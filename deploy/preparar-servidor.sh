#!/bin/sh
# Prepara um servidor Ubuntu novo para rodar a Livredo.
#
# Rode uma vez, como root, dentro da pasta do projeto no servidor:
#   sudo sh deploy/preparar-servidor.sh
#
# O que faz:
#   1. instala Docker, git e o firewall;
#   2. deixa abertas só as portas de SSH, HTTP e HTTPS;
#   3. liga as atualizações automáticas de segurança do Ubuntu;
#   4. cria 2 GB de swap se a máquina tiver pouca memória (o build do Go precisa);
#   5. agenda o backup diário do banco às 3h, guardando os últimos 14 dias.
#
# Pode ser rodado de novo sem problema: cada passo confere se já foi feito.

set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "Rode como root: sudo sh deploy/preparar-servidor.sh" >&2
	exit 1
fi

PROJETO="$(cd "$(dirname "$0")/.." && pwd)"
echo "==> Projeto em $PROJETO"

echo "==> Pacotes básicos"
apt-get update -q
DEBIAN_FRONTEND=noninteractive apt-get install -y -q ca-certificates curl git ufw unattended-upgrades

echo "==> Docker"
if ! command -v docker >/dev/null 2>&1; then
	curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker

echo "==> Firewall: só SSH, HTTP e HTTPS"
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw --force enable

echo "==> Atualizações automáticas de segurança"
dpkg-reconfigure -f noninteractive unattended-upgrades

echo "==> Swap"
MEMORIA_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
if [ "$MEMORIA_MB" -lt 3000 ] && ! swapon --show | grep -q .; then
	fallocate -l 2G /swapfile
	chmod 600 /swapfile
	mkswap /swapfile
	swapon /swapfile
	grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
	echo "    2 GB de swap criados (memória: ${MEMORIA_MB} MB)"
else
	echo "    não é necessário"
fi

echo "==> Backup diário às 3h (mantém 14 dias em $PROJETO/backups)"
mkdir -p "$PROJETO/backups"
# O container roda como o usuário "loja" (uid 10001, ver Dockerfile), não como
# root: sem isso ele não consegue gravar o backup na pasta.
chown 10001:10001 "$PROJETO/backups"
chmod 750 "$PROJETO/backups"
cat > /etc/cron.d/livredo-backup <<EOF
0 3 * * * root cd "$PROJETO" && docker compose run --rm backup >> /var/log/livredo-backup.log 2>&1 && find "$PROJETO/backups" -name 'loja-*.db' -mtime +14 -delete
EOF
chmod 644 /etc/cron.d/livredo-backup

echo
echo "Servidor pronto. Próximos passos:"
echo "  1. crie o arquivo .env (veja o README, seção Deploy)"
echo "  2. docker compose up -d --build"
