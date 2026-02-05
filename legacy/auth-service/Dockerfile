FROM adoptopenjdk/openjdk14:jre-14.0.2_12-alpine

RUN apk --no-cache add curl
COPY target/auth-service*.jar auth-service.jar
ENV JAVA_OPTS="-Xmx512m -Xms256m -XX:+UseG1GC -XX:MaxGCPauseMillis=200"
CMD ["sh", "-c", "java ${JAVA_OPTS} -jar --enable-preview auth-service.jar"]
