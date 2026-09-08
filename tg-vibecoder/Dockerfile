FROM python:3.12-slim

WORKDIR /app

COPY tg-vibecoder/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY agentkit /app/agentkit
COPY tg-vibecoder /app

ENV AGENTKIT_PATH=/app/agentkit
ENV PORT=8000
EXPOSE 8000

CMD ["python", "server.py"]
