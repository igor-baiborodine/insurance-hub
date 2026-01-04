yarn --cwd legacy/web-vue install
[ $? -eq 0 ] || exit 1
yarn --cwd legacy/web-vue run build
[ $? -eq 0 ] || exit 1