# Build context is java-server/notification-service. Build and run from the repository root with:
#   docker compose up --build java-notification-service
FROM maven:3.9-eclipse-temurin-17 AS build
WORKDIR /build
COPY pom.xml .
RUN mvn -B -q dependency:go-offline
COPY src ./src
# Tests run in CI and locally (see README); the image build only packages.
RUN mvn -B -q package -DskipTests -Djacoco.skip=true

FROM eclipse-temurin:17-jre-jammy
# The release this image belongs to; /health reports it. Set by the publish workflow from the VERSION file.
ARG VERSION=dev
ENV APP_VERSION=$VERSION
LABEL org.opencontainers.image.version=$VERSION
RUN useradd --system --uid 10001 appuser
WORKDIR /app
COPY --from=build /build/target/notification-service.jar /app/notification-service.jar
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /health HTTP/1.0\r\n\r\n" >&3 && grep -q "200 OK" <&3' || exit 1
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75", "-jar", "/app/notification-service.jar"]
