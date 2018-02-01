FROM alpine:3.6
MAINTAINER source{d}

RUN apk add --no-cache ca-certificates dumb-init=1.2.0-r0 git

RUN mkdir -p /opt/borges-export

WORKDIR /opt/borges-export

ADD borges-export /bin/
ADD set-forks /bin/

ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["borges-export", "-debug"]
