 commit=$(git rev-parse --short HEAD)
 docker build . -t paulgmiller/letsmeetup:$commit
 docker push paulgmiller/letsmeetup:$commit