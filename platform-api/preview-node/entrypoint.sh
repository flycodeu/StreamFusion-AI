#!/bin/sh
set -eu
umask 077
config=/config/node.json
java -jar /opt/preview/preview-node.jar --render "$config"
java -jar /opt/preview/preview-node.jar --firewall "$config"
mkdir -p /state/ownership /state/media-log
chown -R 10001:10001 /state
# Drop all inherited capabilities after configuring only this container's network namespace.
exec setpriv --reuid=10001 --regid=10001 --clear-groups --bounding-set=-all /bin/sh -c '
  set -eu
  /opt/media/MediaServer -c /state/config.ini -l 4 --log-dir /state/media-log --log-size 1 --log-slice 2 -t 4 >/dev/null 2>&1 &
  media=$!
  ready=0
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if curl --silent --max-time 1 --output /dev/null http://127.0.0.1:8080/index/api/getServerConfig; then ready=1; break; fi
    kill -0 "$media" 2>/dev/null || exit 1
    sleep 1
  done
  if [ "$ready" != 1 ]; then kill -TERM "$media"; exit 1; fi
  java -Dlogback.configurationFile=/opt/preview/logback-node.xml -jar /opt/preview/preview-node.jar /config/node.json &
  control=$!
  trap "kill -TERM $control $media 2>/dev/null || true; wait || true" INT TERM EXIT
  # Either process exiting stops the appliance. The next boot reconciles durable ownership.
  while kill -0 "$media" 2>/dev/null && kill -0 "$control" 2>/dev/null; do sleep 1; done
  exit 1
'
