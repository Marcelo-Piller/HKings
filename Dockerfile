# ─── Build ───────────────────────────────────────────────────────────────────
# A imagem precisa ser igual ou superior ao "go" declarado em go.mod (1.26, por
# causa do pdfcpu da amostra de e-book). A versão
# anterior era golang:1.22 contra um go.mod que exige 1.25, e o build falhava
# logo no "go mod download".
FROM golang:1.26-alpine AS builder

WORKDIR /app

# As dependências são baixadas numa camada própria, aproveitando o cache
# enquanto go.mod e go.sum não mudarem.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# modernc.org/sqlite é SQLite reescrito em Go puro: não precisa de cgo, gcc nem
# musl-dev. Com CGO_ENABLED=0 o binário sai estático e a imagem final fica menor.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o server .

# ─── Execução ────────────────────────────────────────────────────────────────
FROM alpine:3.20

# ca-certificates: chamadas HTTPS ao Mercado Pago
# tzdata: datas dos pedidos no horário de Brasília
# wget: usado pelo HEALTHCHECK
RUN apk add --no-cache ca-certificates tzdata wget

# Usuário sem privilégios: um problema na aplicação não vira root no container.
RUN addgroup -g 10001 -S loja && adduser -u 10001 -S loja -G loja

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# /data guarda o banco e /app/static/img/uploads as fotos dos produtos. Os dois
# são montados como volumes no compose; sem isso, recriar o container apaga
# pedidos, clientes e imagens.
RUN mkdir -p /data /app/static/img/uploads \
    && chown -R loja:loja /data /app/static/img/uploads

USER loja

ENV PORT=8080 \
    APP_ENV=production \
    DB_PATH=/data/loja.db \
    TZ=America/Sao_Paulo

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

CMD ["./server"]
