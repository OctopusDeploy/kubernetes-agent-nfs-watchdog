package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"
	v1Api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Set up signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Check to see if there's missing environment variables
	missingEnvVars := checkForRequiredEnvironmentVariables()
	if missingEnvVars != nil {
		panic(missingEnvVars)
	}
	directory := os.Getenv("watchdog_directory")
	podName := os.Getenv("HOSTNAME")

	// Optional environment variables
	checkInterval := time.Duration(setVarWithDefault("watchdog_loop_seconds", 5) * float64(time.Second.Nanoseconds()))
	initialBackoff := time.Duration(setVarWithDefault("watchdog_initial_backoff_seconds", 0.5) * float64(time.Second.Nanoseconds()))
	timeout := time.Duration(setVarWithDefault("watchdog_timeout_seconds", 10) * float64(time.Second.Nanoseconds()))

	// Check and initialise the kubernetes client
	clientset, err := tryGetKubernetesClient()
	if err != nil {
		panic(err)
	}

	currentNamespace, err := tryGetNamespace()
	operation := func() error {
		return checkFilesystem(directory)
	}

	// Get the event recorder
	eventRecorder := getEventRecorder(ctx, *clientset, currentNamespace)

	ticker := time.NewTicker(checkInterval)

	log.Info("Starting Kubernetes Agent NFS Watchdog")
	for range ticker.C {
		log.Info("Checking for read access...")
		fsErr := backoff.Retry(operation, backoff.NewExponentialBackOff(backoff.WithInitialInterval(initialBackoff), backoff.WithMaxElapsedTime(timeout)))
		if fsErr != nil {
			eventErr := raiseNfsWatchDogEvent(clientset, currentNamespace, podName, eventRecorder)
			if eventErr != nil {
				log.Error(eventErr.Error())
			}
			deleteErr := deletePod(clientset, currentNamespace, podName)
			if deleteErr != nil {
				panic(deleteErr)
			}
			return
		}
	}
}

func checkForRequiredEnvironmentVariables() error {
	variables := make([]string, 0)
	missingVariables := make([]string, 0)

	variables = append(variables, "watchdog_directory", "HOSTNAME")

	for _, v := range variables {
		if os.Getenv(v) == "" {
			missingVariables = append(missingVariables, v)
		}
	}

	if len(missingVariables) > 0 {
		return errors.New("Could not start! Missing environment variable(s): " + fmt.Sprintf("%s", missingVariables))
	}

	return nil
}

func setVarWithDefault(environmentVariable string, defaultValue float64) float64 {
	if value, ok := os.LookupEnv(environmentVariable); ok {
		if i, err := strconv.ParseFloat(value, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func tryGetKubernetesClient() (*kubernetes.Clientset, error) {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	// creates the clientset
	return kubernetes.NewForConfig(config)
}

func tryGetNamespace() (string, error) {
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if ns := strings.TrimSpace(string(data)); len(ns) > 0 {
			return ns, nil
		} else {
			return "", errors.New("length of namespace was 0")
		}
	} else {
		return "", err
	}
}

func checkFilesystem(path string) error {
	_, err := os.ReadDir(path)
	if IsCorruptedMnt(err) {
		return errors.New("filesystem is corrupted")
	}
	return nil
}

func deletePod(clientset *kubernetes.Clientset, namespace string, podName string) error {
	deletePolicy := metav1.DeletePropagationForeground
	return clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
}

func getEventRecorder(ctx context.Context, clientset kubernetes.Clientset, currentNamespace string) record.EventRecorderLogger {
	eventBroadcaster := record.NewBroadcaster(record.WithContext(ctx))
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events(currentNamespace)})
	return eventBroadcaster.NewRecorder(scheme.Scheme, v1Api.EventSource{Component: "NfsWatchdog"})
}

func raiseNfsWatchDogEvent(clientset *kubernetes.Clientset, namespace string, podName string, recorder record.EventRecorderLogger) error {
	pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	recorder.Event(pod, v1Api.EventTypeWarning, "NfsWatchdogTimeout", "Stale NFS mount detected, deleting pod")
	return nil
}

func IsCorruptedMnt(err error) bool {
	if err == nil {
		return false
	}

	log.WithError(err).Error("Encountered error checking filesystem")

	var underlyingError error
	switch e := err.(type) {
	case nil:
		return false
	case *os.PathError:
		underlyingError = e.Err
	case *os.LinkError:
		underlyingError = e.Err
	case *os.SyscallError:
		underlyingError = e.Err
	case syscall.Errno:
		underlyingError = err
	}

	return errors.Is(underlyingError, syscall.ESTALE) || errors.Is(underlyingError, syscall.ENOTCONN) || errors.Is(underlyingError, syscall.EIO) || errors.Is(underlyingError, syscall.EACCES) || errors.Is(underlyingError, syscall.EHOSTDOWN) || errors.Is(underlyingError, syscall.EWOULDBLOCK)
}
