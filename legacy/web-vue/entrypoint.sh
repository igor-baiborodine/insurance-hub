#!/bin/bash

envsubst < /etc/nginx/conf.d/default.tmpl '${GATEWAY_URL},${AUTH_URL},${CHAT_URL}' > /etc/nginx/conf.d/default.conf

until nginx -t; do
  echo "Config invalid, waiting..."
  sleep 2
done
nginx -s reload || true

exec nginx -g 'daemon off;'