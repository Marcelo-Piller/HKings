#!/bin/sh
# Publica a versão atual do código em livredo.com.br.
#
# Rode no seu computador, na pasta do projeto (Git Bash no Windows):
#   sh deploy/publicar.sh
#
# Envia só o código (a mesma lista de arquivos do Git, respeitando o
# .gitignore) e reconstrói a loja no servidor. O .env, o banco, as fotos
# enviadas e os backups ficam no servidor e não são tocados.

set -eu

SERVIDOR="${LIVREDO_SERVIDOR:-root@179.236.227.251}"
CHAVE="${LIVREDO_CHAVE:-$HOME/.ssh/livredo_deploy}"
DESTINO=/opt/livredo

cd "$(dirname "$0")/.."

echo "==> Conferindo o código (go vet + testes)"
go vet ./...
go test ./... >/dev/null

echo "==> Empacotando"
LISTA=$(mktemp)
PACOTE=$(mktemp)
trap 'rm -f "$LISTA" "$PACOTE"' EXIT
git ls-files -co --exclude-standard | sort -u | while read -r f; do
	[ -f "$f" ] && echo "$f"
done | grep -v '^\.claude/' | grep -v '^static/img/uploads/' > "$LISTA"
# Trava de segurança: nada de segredos, bancos ou backups no pacote.
if grep -qE '(^|/)\.env$|\.db$|\.sqlite|^backups/|^data/' "$LISTA"; then
	echo "Arquivo sensível na lista de envio; publicação cancelada:" >&2
	grep -E '(^|/)\.env$|\.db$|\.sqlite|^backups/|^data/' "$LISTA" >&2
	exit 1
fi
tar -czf "$PACOTE" -T "$LISTA"

echo "==> Enviando para $SERVIDOR"
scp -i "$CHAVE" -q "$PACOTE" "$SERVIDOR:/root/livredo-publicar.tgz"

echo "==> Reconstruindo no servidor"
ssh -i "$CHAVE" -o BatchMode=yes "$SERVIDOR" "set -e
	cd $DESTINO
	antes=\$(sha256sum Caddyfile)
	tar -xzf /root/livredo-publicar.tgz && rm /root/livredo-publicar.tgz
	docker compose up -d --build --quiet-pull </dev/null >/tmp/livredo-publicar.log 2>&1 || { tail -30 /tmp/livredo-publicar.log; exit 1; }
	# O Caddyfile é montado como arquivo: só um restart faz o Caddy ler a versão nova.
	if [ \"\$antes\" != \"\$(sha256sum Caddyfile)\" ]; then echo 'Caddyfile mudou: reiniciando o Caddy'; docker compose restart caddy >/dev/null; fi
	docker image prune -f >/dev/null
	docker compose ps --format '{{.Service}}: {{.Status}}'"

echo "==> Conferindo https://livredo.com.br"
sleep 5
curl -fsS -o /dev/null -w "HTTP %{http_code}\n" https://livredo.com.br/healthz
