FROM golang as build-env
RUN mkdir /build 
WORKDIR /build 
ADD go.* /build/
RUN go mod download
ADD . /build/
RUN go build -o /guests ./
FROM gcr.io/distroless/base
COPY --from=build-env /guests /
ADD index.html /index.html
ENTRYPOINT ["/guests"]
