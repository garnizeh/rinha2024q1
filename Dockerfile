FROM golang:1.22-alpine AS build
RUN apk update && apk upgrade && apk add --no-cache bash openssh dumb-init
WORKDIR /app
COPY go.mod go.sum main.go ./
RUN go mod download
RUN GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o rinha ./main.go

FROM scratch as release
WORKDIR /
COPY --from=build /usr/bin/dumb-init /usr/bin/dumb-init
COPY --from=build /app/rinha /rinha
EXPOSE 8080
ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["./rinha"]