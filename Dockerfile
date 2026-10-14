FROM golang:1.27.2 AS build-env
RUN mkdir /build 
WORKDIR /build 
ADD go.* /build/
RUN go mod download
ADD . /build/
RUN go build -o /guests ./
FROM gcr.io/distroless/base
COPY --from=build-env /guests /
ADD static/ /static/
ENTRYPOINT ["/guests"]
