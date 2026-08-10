FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/paysplit-api ./cmd/api

FROM alpine:3.22
RUN adduser -D -H app
USER app
COPY --from=build /bin/paysplit-api /usr/local/bin/paysplit-api
EXPOSE 8080
ENTRYPOINT ["paysplit-api"]
