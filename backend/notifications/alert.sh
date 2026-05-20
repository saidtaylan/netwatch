#!/bin/sh
# netwatch alert script
# Called on state change. All alert context is available as env vars.
# Customize this script to send to Slack, PagerDuty, etc.

TIMESTAMP=$(date '+%Y-%m-%dT%H:%M:%S')

echo "[$TIMESTAMP] ALERT node=${NODE_NAME} status=${STATUS} target=${NAME} type=${TYPE} seq=${SEQ} scope=${SCOPE:-local} apps=${AFFECTED_APPS:-none} error=${ERROR_CODE:-}"
