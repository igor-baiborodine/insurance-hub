FROM eclipse-temurin:17-jre-alpine

RUN apk --no-cache add curl
COPY target/payment-service*.jar payment-service.jar
ENV JAVA_OPTS="-Xmx512m -Xms256m -XX:+UseG1GC -XX:MaxGCPauseMillis=200 -XX:+HeapDumpOnOutOfMemoryError -XX:HeapDumpPath=/tmp/heapdump.hprof"
CMD ["sh", "-c", "java ${JAVA_OPTS} -jar payment-service.jar"]
