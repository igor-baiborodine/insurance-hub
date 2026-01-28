FROM adoptopenjdk/openjdk14:jre-14.0.2_12-alpine

RUN apk --no-cache add curl
COPY target/policy-service*.jar policy-service.jar
ENV JAVA_OPTS="-XX:+UseContainerSupport -XX:MaxRAMPercentage=75.0 -XX:+UseG1GC -XX:MaxGCPauseMillis=200"
CMD ["sh", "-c", "java ${JAVA_OPTS} -jar policy-service.jar"]
