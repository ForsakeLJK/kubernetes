# Compiler, libc, OpenMP runtime and Python are fixed by the multi-platform digest.
FROM gcc:14.2.0-bookworm@sha256:b99b86a28812b1e6453a231a947dc43d76fe192788a12f344a9b568bf9f5d24c
WORKDIR /opt/stream
COPY source.json base-image.txt run.py parse.py observe.c observe.h ./
COPY upstream/stream.c upstream/stream.c
RUN python3 --version && gcc --version && python3 -c 'import hashlib,json; assert hashlib.sha256(open("upstream/stream.c","rb").read()).hexdigest() == json.load(open("source.json"))["sha256"]'
LABEL org.opencontainers.image.title="Instrumented STREAM 5.10 workload" \
      org.opencontainers.image.description="Variant of STREAM with an untimed initialization observation hook" \
      io.stream.source.sha256="a52bae5e175bea3f7832112af9c085adab47117f7d2ce219165379849231692b"
ENTRYPOINT ["python3", "/opt/stream/run.py"]
