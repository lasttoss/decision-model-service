# Two stages, and the second one carries the case set and the baseline: the evaluation has to
# run inside the image, offline, because that is how the CI job proves the numbers are
# reproducible rather than remembered.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/decide ./cmd/decide

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/decide /usr/local/bin/decide
COPY eval /app/eval
COPY testdata /app/testdata
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/decide"]
CMD ["--mode", "server"]
