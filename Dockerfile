FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hello-guest ./cmd/hello-guest

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/hello-guest /usr/local/bin/hello-guest
EXPOSE 80
VOLUME /app/data
ENTRYPOINT ["hello-guest", "--data", "/app/data"]
