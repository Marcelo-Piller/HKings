# Como rodar o Site de Vendas

Guia rápido. Para detalhes de configuração, pagamento, backup e deploy, veja o [README.md](README.md).

---

## Opção 1 — Docker (recomendado)

Precisa apenas de Docker e Docker Compose.

### 1. Crie o `.env`

```bash
cp .env.example .env
```

Gere os segredos:

```bash
openssl rand -base64 48   # cole em SESSION_SECRET
openssl rand -base64 32   # cole em SETTINGS_ENCRYPTION_KEY
```

Preencha também no `.env`:

```env
ADMIN_USERNAME=admin
ADMIN_PASSWORD=uma-senha-com-12-caracteres-ou-mais
```

> Não existem contas padrão. O primeiro administrador nasce dessas duas variáveis.

### 2. Suba

```bash
docker compose up -d --build
```

| | Endereço |
|---|---|
| Loja | http://localhost:8080 |
| Admin | http://localhost:8080/admin/login |
| Vendedor | http://localhost:8080/vendedor/login |

### 3. Comandos úteis

```bash
docker compose logs -f loja      # logs
docker compose restart loja      # reiniciar
docker compose down              # parar (dados preservados nos volumes)
docker compose run --rm backup   # backup em ./backups
```

---

## Opção 2 — Direto com Go

Precisa de **Go 1.25+**. Não precisa de GCC: o driver SQLite é Go puro.

### Windows (PowerShell)

```powershell
$env:SESSION_SECRET = "coloque-aqui-32-caracteres-ou-mais"
$env:SETTINGS_ENCRYPTION_KEY = "chave-base64-de-32-bytes"
$env:ADMIN_USERNAME = "admin"
$env:ADMIN_PASSWORD = "uma-senha-com-12-caracteres"
go run .
```

### Linux / macOS

```bash
export SESSION_SECRET="$(openssl rand -base64 48)"
export SETTINGS_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export ADMIN_USERNAME="admin"
export ADMIN_PASSWORD="uma-senha-com-12-caracteres"
go run .
```

Acesse http://localhost:8080.

Fora de `APP_ENV=production`, o `SESSION_SECRET` é opcional — a aplicação gera um temporário e
avisa no log. As sessões caem a cada reinício.

O banco é criado em `./data/loja.db`. Para mudar, defina `DB_PATH`.

---

## Primeiros passos no painel

1. Entre em `/admin/login` com o usuário e a senha do `.env`.
2. **Categorias** — cadastre as categorias da sua loja. É essa lista que os formulários de produto
   oferecem; o vendedor não digita categoria à mão.
3. **Configurações** — cole o Access Token e a chave secreta do webhook do Mercado Pago, e informe
   a URL base da loja.
4. **Produtos** — cadastre os produtos, ou deixe cada vendedor cadastrar os seus.

O vendedor não precisa que você crie a conta dele: ele mesmo se cadastra em `/vendedor/cadastro`
com nome, usuário e senha (12+ caracteres), e já entra ativo no painel. Você também pode criar
contas manualmente em **Vendedores**, se preferir.

---

## Testando pagamento localmente

O webhook do Mercado Pago precisa de uma URL pública. Use um túnel:

```bash
ngrok http 8080
```

Copie a URL `https://...ngrok...` e informe em **Configurações → URL Base da Loja**. Cadastre
`https://...ngrok.../webhook/mp` no painel do Mercado Pago e cole a chave secreta gerada lá.

Sem isso, o pedido é criado mas nunca é confirmado como pago.

---

## Rodar os testes

```bash
go test ./...
```

---

## Problemas comuns

| Erro | Solução |
|---|---|
| `go.mod requires go >= 1.25.0` | Atualize o Go, ou use Docker. |
| `SESSION_SECRET é obrigatório em produção` | Defina a variável com 32+ caracteres. |
| `configure ADMIN_USERNAME e ADMIN_PASSWORD` | Senha precisa de 12 caracteres ou mais. |
| Webhook responde 503 | Falta `MERCADOPAGO_WEBHOOK_SECRET`. |
| Webhook responde 401 | A chave secreta não confere com a do Mercado Pago. |
| Pedido não vira "pago" | Webhook não cadastrado ou URL base errada. Veja os logs. |
