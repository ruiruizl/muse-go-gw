FROM golang:1.24-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
RUN git clone --depth 1 https://github.com/CoolkHz/muse-protocol-go sdk
COPY go.mod main.go ./
RUN go build -trimpath -ldflags "-s -w" -o /muse-go-gw .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /muse-go-gw .
ENV MUSE_COOKIE_FILE=/app/cookies.txt \
    MUSE_STATE_DIR=/app/.muse-state \
    DATA_DIR=/app/data \
    PORT=8080
EXPOSE 8080
CMD ["/app/muse-go-gw"]
