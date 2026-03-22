#!/bin/bash
# Disk usage monitor for Tabulum VPS
# Install as cron job: 0 */6 * * * /opt/tabulum/scripts/disk_monitor.sh
#
# Alerts by writing to syslog when disk usage exceeds thresholds.
# Check with: journalctl -t tabulum-disk-monitor

WARN_THRESHOLD=80
CRIT_THRESHOLD=90

usage=$(df / --output=pcent | tail -1 | tr -d ' %')

if [ "$usage" -ge "$CRIT_THRESHOLD" ]; then
    logger -t tabulum-disk-monitor -p user.crit "CRITICAL: Disk usage at ${usage}% — immediate action required"
elif [ "$usage" -ge "$WARN_THRESHOLD" ]; then
    logger -t tabulum-disk-monitor -p user.warning "WARNING: Disk usage at ${usage}%"
fi

# Also check event log directory specifically
if [ -d /opt/tabulum/data/eventlog ]; then
    eventlog_size=$(du -sm /opt/tabulum/data/eventlog 2>/dev/null | cut -f1)
    if [ "${eventlog_size:-0}" -ge 500 ]; then
        logger -t tabulum-disk-monitor -p user.warning "WARNING: Event log at ${eventlog_size}MB"
    fi
fi
