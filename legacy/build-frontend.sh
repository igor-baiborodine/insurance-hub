yarn --cwd legacy/web-vue install
[ $? -eq 0 ] || exit 1
VUE_APP_BACKEND_URL= VUE_APP_AUTH_URL= VUE_APP_CHAT_URL= yarn --cwd legacy/web-vue run build
[ $? -eq 0 ] || exit 1