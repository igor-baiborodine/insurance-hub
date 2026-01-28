FROM eclipse-temurin:17-jre-alpine

RUN apk --no-cache add curl
COPY target/product-service*.jar product-service.jar
ENV JAVA_OPTS="-XX:+UseContainerSupport -XX:MaxRAMPercentage=75.0 -XX:+UseG1GC"
CMD ["sh", "-c", "java ${JAVA_OPTS} -jar product-service.jar"]
