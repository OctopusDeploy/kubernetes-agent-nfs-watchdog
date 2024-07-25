# Kubernetes agent NFS Watchdog

The Kubernetes agent NFS Watchdog is a small application that monitors the status of the connection to the Kubernetes agent NFS pod. If this connection is interrupted, it forcibly terminates the Kubernetes agent pod to protect against data corruption.
