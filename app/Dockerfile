FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN go build -trimpath -ldflags="-s -w" -o /out/sample-platform-app .

FROM alpine:3.22
RUN adduser -D -H appuser
USER appuser
WORKDIR /app
COPY --from=build /out/sample-platform-app /app/sample-platform-app
EXPOSE 8080
ENTRYPOINT ["/app/sample-platform-app"]
