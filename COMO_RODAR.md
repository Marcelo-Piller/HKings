# Como Rodar o Site de Vendas

## Pré-requisitos
- Go 1.22+ instalado (https://go.dev/dl/)
- GCC (para compilar o driver SQLite)
  - Windows: instale o TDM-GCC ou use o MSYS2

## Rodando Localmente

```bash
cd "c:/Projeto/Site de vendas"
go mod tidy
go run .
```

Acesse: http://localhost:8080
Admin:   http://localhost:8080/admin/login  (admin / admin123)

## Rodando com Docker

```bash
docker build -t minhaloja .
docker run -p 8080:8080 -v $(pwd)/data:/app/data minhaloja
```

## Variáveis de Ambiente
| Variável | Padrão | Descrição |
|----------|--------|-----------|
| PORT     | 8080   | Porta do servidor |

## Estrutura do Projeto
```
.
├── main.go              # Entry point
├── database/db.go       # SQLite + migrations
├── models/              # Product, Cart, Order
├── handlers/            # HTTP handlers (loja + admin)
├── templates/           # HTML templates (Tailwind CSS)
├── static/              # Arquivos estáticos (CSS, JS, imagens)
└── Dockerfile           # Build para produção
```

## Funcionalidades
- ✅ Catálogo de produtos com categorias e busca
- ✅ Página de detalhes do produto
- ✅ Carrinho de compras (sessão de cookie)
- ✅ Checkout e confirmação de pedido
- ✅ Painel Admin completo (produtos + pedidos)
- ✅ Banco de dados SQLite (zero configuração)
