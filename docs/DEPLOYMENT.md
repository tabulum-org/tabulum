# Tabulum Kernel — Deployment Notes

## Event Log Disk Management

- The event log grows continuously (append-only, never deleted by the kernel)
- Log rotation creates new files but old files are retained indefinitely
- The operator is responsible for monitoring disk usage on the event log directory
- Consider placing the event log on a separate disk/volume from the bbolt databases
- Compressed archival of rotated log files is recommended for long-running deployments
