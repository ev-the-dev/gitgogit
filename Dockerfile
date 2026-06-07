FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gitgogit .

FROM alpine:3.21
RUN apk add --no-cache git ca-certificates openssh-client \
    && adduser -D -h /home/gitgogit gitgogit
USER gitgogit
ENV HOME=/home/gitgogit
RUN mkdir -p /home/gitgogit/.config/gitgogit \
             /home/gitgogit/.local/share/gitgogit/repos
COPY --from=build /gitgogit /usr/local/bin/gitgogit
ENTRYPOINT ["gitgogit", "--daemon-child", "--config", "/home/gitgogit/.config/gitgogit/config.yaml"]
