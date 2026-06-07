FROM python@sha256:c845af9399020c7e562969a13689e929074a10fd057acd1b1fad06a2fb068e97
RUN mkdir -p /input /sandbox
COPY wrapper.sh /wrapper.sh
RUN chmod +x /wrapper.sh
ENTRYPOINT ["/wrapper.sh"]
CMD ["python", "/sandbox/main.py"]
