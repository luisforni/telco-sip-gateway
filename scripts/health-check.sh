#!/bin/bash

check_kamailio_status() {
    systemctl is-active --quiet kamailio
    return $?
}

check_connectivity() {
    ping -c 1 8.8.8.8 > /dev/null 2>&1
    return $?
}

check_kamailio_status
KAMAILIO_STATUS=$?

check_connectivity
CONNECTIVITY_STATUS=$?

if [ $KAMAILIO_STATUS -ne 0 ]; then
    echo "Kamailio service is down."
    exit 1
fi

if [ $CONNECTIVITY_STATUS -ne 0 ]; then
    echo "Network connectivity is down."
    exit 2
fi

echo "Health check passed. Kamailio service is up and running with network connectivity."
exit 0
