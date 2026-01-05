FROM nginx
MAINTAINER ASC-LAB

COPY ./dist /usr/share/nginx/html

COPY nginx-app.conf /etc/nginx/conf.d/default.tmpl

COPY entrypoint.sh /

ENTRYPOINT ["/entrypoint.sh"]
