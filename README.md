# 🛍️ MinhaLoja — Site de Vendas em Go

Site de e-commerce completo construído com Go puro (sem frameworks), SQLite e Tailwind CSS. Inclui loja pública, carrinho, checkout com integração InfinitPay e painel administrativo completo.

---

## Sumário

- [Funcionalidades](#funcionalidades)
- [Tecnologias](#tecnologias)
- [Estrutura do Projeto](#estrutura-do-projeto)
- [Pré-requisitos](#pré-requisitos)
- [Instalação e Execução](#instalação-e-execução)
- [Variáveis de Ambiente](#variáveis-de-ambiente)
- [Rotas da Aplicação](#rotas-da-aplicação)
- [Painel Administrativo](#painel-administrativo)
- [Integração com InfinitPay](#integração-com-infinitpay)
- [Upload de Imagens](#upload-de-imagens)
- [Deploy](#deploy)
- [Custo de Hospedagem](#custo-de-hospedagem)

---

## Funcionalidades

### Loja Pública
- Catálogo de produtos com filtro por categoria e busca por texto
- Página de detalhes de cada produto
- Carrinho de compras persistido em cookie de sessão (adicionar, alterar quantidade, remover)
- Checkout com formulário de dados do cliente
- Integração com InfinitPay para pagamento online (PIX, cartão, boleto)
- Página de confirmação de pedido

### Painel Administrativo (`/admin`)
- Dashboard com totais de pedidos, receita e produtos
- CRUD completo de produtos (criar, editar, ativar/desativar)
- Upload de imagem de produto (JPG, PNG, WEBP, GIF — até 5MB)
- Listagem de pedidos com status
- Alteração de status de pedido (pendente → pago → enviado → entregue)
- Login/logout com autenticação por sessão

---

## Tecnologias

| Tecnologia | Uso |
|-----------|-----|
| Go 1.22+ | Linguagem principal / servidor HTTP |
| `net/http` | Roteamento e handlers (sem framework externo) |
| `html/template` | Renderização de páginas HTML |
| `modernc.org/sqlite` | Banco de dados SQLite (pure Go, sem CGO) |
| `gorilla/sessions` | Gerenciamento de sessões com cookie |
| Tailwind CSS (CDN) | Estilização das páginas |
| InfinitPay API | Gateway de pagamento brasileiro |

---

## Estrutura do Projeto

```
Site de vendas/
├── main.go                     # Entry point: rotas e inicialização
├── go.mod / go.sum             # Dependências Go
├── loja.db                     # Banco SQLite (criado automaticamente)
│
├── database/
│   └── db.go                   # Conexão SQLite + migrations + seed inicial
│
├── models/
│   ├── product.go              # Model de Produto (CRUD)
│   ├── cart.go                 # Model de Carrinho (em memória/sessão)
│   └── order.go                # Model de Pedido (CRUD)
│
├── handlers/
│   ├── store.go                # Handlers da loja pública + sistema de templates
│   ├── admin.go                # Handlers do painel admin + middleware de auth
│   └── session.go              # Configuração da sessão de cookie
│
├── services/
│   └── payment.go              # Integração com InfinitPay API
│
├── templates/
│   ├── layout.html             # Layout base da loja (header, footer, nav)
│   ├── home.html               # Página inicial com grid de produtos
│   ├── product.html            # Página de detalhes do produto
│   ├── cart.html               # Carrinho de compras
│   ├── checkout.html           # Formulário de checkout
│   ├── order_confirm.html      # Confirmação de pedido
│   ├── admin_login.html        # Tela de login do admin
│   ├── admin_dashboard.html    # Dashboard do admin
│   ├── admin_products.html     # Listagem de produtos no admin
│   ├── admin_product_form.html # Formulário de criar/editar produto
│   ├── admin_orders.html       # Listagem de pedidos no admin
│   ├── admin_order_detail.html # Detalhe + troca de status do pedido
│   └── admin/
│       └── layout.html         # Layout base do painel admin (sidebar)
│
└── static/
    └── img/
        ├── placeholder.svg     # Imagem padrão para produtos sem foto
        └── uploads/            # Imagens enviadas pelo admin (criado auto.)
```

---

## Pré-requisitos

- **Go 1.22 ou superior** — [Download](https://go.dev/dl/)
- Sem necessidade de GCC/CGO (SQLite é pure Go via `modernc.org/sqlite`)
- Conexão com internet apenas para carregar o Tailwind CSS via CDN

---

## Instalação e Execução

### 1. Clone ou baixe o projeto

```bash
cd "c:/Projeto/Site de vendas"
```

### 2. Instale as dependências

```bash
go mod tidy
```

### 3. Execute o servidor

```bash
go run .
```

O banco de dados `loja.db` é criado automaticamente com 6 produtos de exemplo na primeira execução.

### 4. Acesse no navegador

| URL | Descrição |
|-----|-----------|
| `http://localhost:8080` | Loja pública |
| `http://localhost:8080/admin/login` | Painel admin |

**Credenciais padrão do admin:** `admin` / `admin123`

> ⚠️ Troque a senha padrão em produção diretamente no banco: `UPDATE admins SET password='nova_senha' WHERE username='admin';`

---

## Variáveis de Ambiente

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `PORT` | `8080` | Porta do servidor HTTP |
| `INFINITPAY_API_KEY` | *(vazio)* | Chave de API do InfinitPay para pagamentos |

### Exemplo de uso (PowerShell)

```powershell
$env:PORT = "3000"
$env:INFINITPAY_API_KEY = "sua_chave_aqui"
go run .
```

### Exemplo de uso (bash/Linux/Mac)

```bash
PORT=3000 INFINITPAY_API_KEY="sua_chave_aqui" go run .
```

---

## Rotas da Aplicação

### Loja Pública

| Método | Rota | Descrição |
|--------|------|-----------|
| GET | `/` | Página inicial com lista de produtos |
| GET | `/?categoria=X` | Filtra produtos por categoria |
| GET | `/?q=texto` | Busca produtos por nome/descrição |
| GET | `/produto?id=X` | Página de detalhes do produto |
| GET | `/carrinho` | Visualiza o carrinho |
| POST | `/carrinho/add` | Adiciona produto ao carrinho |
| POST | `/carrinho/update` | Atualiza quantidade no carrinho |
| POST | `/carrinho/remove` | Remove item do carrinho |
| GET | `/checkout` | Formulário de finalização de compra |
| POST | `/checkout` | Processa o pedido e redireciona ao pagamento |
| GET | `/pedido?id=X` | Confirmação do pedido (fallback sem InfinitPay) |

### Painel Admin (requer autenticação)

| Método | Rota | Descrição |
|--------|------|-----------|
| GET/POST | `/admin/login` | Login do administrador |
| POST | `/admin/logout` | Logout |
| GET | `/admin` | Dashboard com métricas |
| GET | `/admin/produtos` | Lista todos os produtos |
| GET/POST | `/admin/produtos/novo` | Cria novo produto |
| GET/POST | `/admin/produtos/editar?id=X` | Edita produto existente |
| POST | `/admin/produtos/excluir` | Desativa produto (soft delete) |
| GET | `/admin/pedidos` | Lista todos os pedidos |
| GET/POST | `/admin/pedidos/detalhe?id=X` | Detalhe e troca de status do pedido |

---

## Painel Administrativo

Acesse `/admin/login` com usuário `admin` e senha `admin123`.

### Gerenciar Produtos

1. Vá em **Produtos → + Novo Produto**
2. Preencha: nome, descrição, preço (em reais), estoque, categoria
3. Faça upload de uma imagem clicando na área pontilhada (opcional)
4. Clique em **Salvar Produto**

Para **editar**, clique em "Editar" na lista de produtos.  
Para **desativar** (ocultar da loja), clique em "Excluir" — o produto não é deletado do banco.

### Gerenciar Pedidos

1. Vá em **Pedidos**
2. Clique em "Ver" no pedido desejado
3. Altere o status: `pendente → pago → enviado → entregue → cancelado`
4. Clique em **Salvar**

---

## Integração com InfinitPay

O InfinitPay é um gateway de pagamento brasileiro que aceita PIX, cartão de crédito e boleto.

### Como configurar

1. **Crie uma conta** em [infinitpay.io](https://infinitpay.io)
2. **Acesse o painel de desenvolvedor** e gere sua API Key
3. **Configure a variável de ambiente** antes de iniciar o servidor:

```powershell
$env:INFINITPAY_API_KEY = "sua_api_key_aqui"
go run .
```

### Comportamento

- **Com InfinitPay configurado:** Ao finalizar o pedido, o cliente é redirecionado para a página de pagamento do InfinitPay.
- **Sem InfinitPay configurado:** O site funciona normalmente — o cliente vê uma página de confirmação de pedido e o dono recebe o pedido no painel admin para combinar o pagamento manualmente.

### Arquivo de integração

`services/payment.go` — contém a função `CreatePaymentLink(amountBRL, description, orderID)` que chama a API REST do InfinitPay.

---

## Upload de Imagens

Imagens enviadas pelo admin são salvas em `static/img/uploads/` com nome único baseado em timestamp.

- **Formatos aceitos:** JPG, JPEG, PNG, WEBP, GIF
- **Tamanho máximo:** 5MB
- **URL pública:** `/static/img/uploads/nome_do_arquivo.jpg`

O diretório de uploads é criado automaticamente na inicialização do servidor.

---

## Deploy

### Docker (recomendado para produção)

```bash
# Build da imagem
docker build -t minhaloja .

# Execução com volume para persistir o banco
docker run -d \
  -p 8080:8080 \
  -v $(pwd)/loja.db:/app/loja.db \
  -v $(pwd)/static/img/uploads:/app/static/img/uploads \
  -e INFINITPAY_API_KEY="sua_chave" \
  --name minhaloja \
  minhaloja
```

### Oracle Cloud (gratuito para sempre)

Oracle Cloud Free Tier oferece 2 CPUs + 12GB RAM permanentemente grátis:

1. Crie uma conta em [oracle.com/cloud/free](https://oracle.com/cloud/free)
2. Crie uma instância VM (Ampere A1 — ARM)
3. Instale Docker na VM
4. Suba a imagem e execute com o comando acima

### Fly.io (gratuito para tráfego baixo)

```bash
fly launch
fly deploy
fly secrets set INFINITPAY_API_KEY="sua_chave"
```

### Variáveis necessárias em produção

| Variável | Obrigatória | Descrição |
|----------|-------------|-----------|
| `PORT` | Não | Porta (padrão: 8080) |
| `INFINITPAY_API_KEY` | Não* | Chave InfinitPay (*necessária para pagamento online) |

> **Importante em produção:** Altere a chave secreta da sessão em `handlers/session.go`:
> ```go
> var store = sessions.NewCookieStore([]byte("TROQUE-POR-UMA-CHAVE-SECRETA-FORTE"))
> ```

---

## Custo de Hospedagem

| Plataforma | Custo/mês | Observação |
|-----------|-----------|------------|
| Oracle Cloud Free Tier | **R$ 0** | ⭐ Melhor opção — 2 CPUs + 12GB RAM, sempre grátis |
| Fly.io Free | **R$ 0** | Bom para testes, pode ter cold start |
| Render.com Free | **R$ 0** | Dorme após 15min inativo |
| Railway.app | ~R$ 5–15 | Fácil de usar, uso contínuo |
| Hetzner CX22 (VPS) | ~R$ 20 | Melhor custo-benefício pago |

Go usa ~15MB de RAM em idle — perfeito para servidores pequenos e gratuitos.

---

## Banco de Dados

O banco SQLite (`loja.db`) é criado automaticamente com as seguintes tabelas:

| Tabela | Descrição |
|--------|-----------|
| `products` | Produtos da loja |
| `orders` | Pedidos realizados |
| `order_items` | Itens de cada pedido |
| `admins` | Usuários administrativos |

Na primeira execução, 6 produtos de exemplo são inseridos automaticamente.

### Acessar o banco diretamente (opcional)

```bash
# Instale o sqlite3 CLI ou use o DB Browser for SQLite
sqlite3 loja.db
.tables
SELECT * FROM products;
```
