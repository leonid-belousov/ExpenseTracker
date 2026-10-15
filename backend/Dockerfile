FROM golang:1.26-alpine3.23 AS build
WORKDIR /src
# Override (e.g. GOPROXY=https://goproxy.io,direct in .env) where the default
# module proxy is unreachable. go.sum still pins every module hash.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/expense-tracker ./cmd/expense-tracker

FROM alpine:3.23
RUN adduser -D -H -u 10001 app
COPY --from=build /out/expense-tracker /usr/local/bin/expense-tracker
USER app
ENTRYPOINT ["/usr/local/bin/expense-tracker"]
CMD ["api"]
