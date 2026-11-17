# 📚 Livredo

*Onde cada história encontra seu próximo leitor.*

Livraria virtual em Go puro (sem framework), com SQLite, Tailwind CSS e pagamentos pelo
**Mercado Pago** (PIX e cartão). Um mercado de livros de leitor para leitor: vitrine pública,
sacola, checkout, área do cliente, painel administrativo e painel do vendedor, onde qualquer
pessoa pode se cadastrar e vender seus livros.

---

## Sumário

- [Visão geral](#visão-geral)
- [Identidade visual](#identidade-visual)
- [Requisitos](#requisitos)
- [Início rápido (Docker)](#início-rápido-docker)
- [Rodando sem Docker](#rodando-sem-docker)
- [Variáveis de ambiente](#variáveis-de-ambiente)
- [Configurando o Mercado Pago](#configurando-o-mercado-pago)
- [Frete e etiquetas (Melhor Envio)](#frete-e-etiquetas-melhor-envio)
- [Login com Google (compradores)](#login-com-google-compradores)
- [Banco de dados](#banco-de-dados)
- [Backup e restauração](#backup-e-restauração)
- [Painéis](#painéis)
- [Rotas](#rotas)
- [Segurança](#segurança)
- [Deploy em produção (livredo.com.br)](#deploy-em-produção-livredocombr)
- [Testes](#testes)
- [Estrutura do projeto](#estrutura-do-projeto)
- [Solução de problemas](#solução-de-problemas)

---

## Visão geral

| Tecnologia | Uso |
|---|---|
| Go 1.25+ | Linguagem e servidor HTTP |
| `net/http` | Roteamento e handlers, sem framework |
| `html/template` | Renderização das páginas |
| `modernc.org/sqlite` | SQLite em Go puro — **não precisa de GCC nem cgo** |
| `gorilla/sessions` | Sessões em cookie assinado |
| `golang.org/x/crypto` | Hash de senha com bcrypt |
| Tailwind CSS (CDN) | Estilos, com paleta própria em `static/js/tailwind-config.js` |
| Fraunces + DM Sans | Tipografia (Google Fonts) |
| Mercado Pago | PIX e Checkout Pro (cartão e boleto) |

### O que cada perfil faz

- **Cliente** — navega, compra, paga com PIX ou cartão e acompanha os pedidos em `/conta`.
- **Vendedor** (`/vendedor`) — cadastra os próprios produtos (com **upload de foto** e categoria
  escolhida em lista) e marca seus pedidos como enviados/entregues. Não vê nem configura nada
  técnico: token, webhook e categorias são só do administrador.
- **Administrador** (`/admin`) — produtos, pedidos, vendedores, categorias e as configurações de
  pagamento e notificação.

---

## Identidade visual

O verde é a essência da marca: o verde-musgo das encadernações clássicas de biblioteca, num tom
saturado. As cores que o acompanham vêm todas do espectro marrom: couro, madeira e papel envelhecido.

| Cor | Tom principal | Onde aparece |
|---|---|---|
| **Musgo** | `#1F5C3C` | Marca, botões principais, cabeçalhos, rodapé |
| **Papel** | `#F7EFE2` | Fundo de toda a loja, com textura sutil de papel |
| **Caramelo** | `#B37F3A` | Ornamentos (✦ ❦), filetes, sublinhados, foco de teclado |
| **Canela** | `#8A4F2F` | Preços, selos e o destaque "Usado" |
| **Camurça** | `#D0B38E` | Superfícies secundárias e brilhos de fundo |
| **Café** | `#30261D` | Texto |

**Tipografia:** *Fraunces* (serifada, com itálico expressivo) nos títulos e *DM Sans* no texto.

**Cabeçalho:** logo, busca e sacola na primeira linha; na segunda, atalhos para *Todo o acervo*,
*Novos*, *Usados*, *Mangá*, *HQ* e *Venda seus livros*. No celular essa linha rola para o lado.

**Detalhes de acabamento:**
- Livros exibidos como capas com lombada, sombra e leve inclinação ao passar o mouse.
- **Capa tipográfica automática** para livros sem foto: título real em itálico, moldura dupla
  dourada e cor escolhida pelo ID, então uma estante sem fotos continua variada.
- Capitular na sinopse, como na abertura de um capítulo.
- Confirmação de pedido no formato de ficha de biblioteca, com carimbo de status.
- Preços no formato brasileiro (`R$ 1.234,56`) em toda a loja e nos painéis.

**Onde mexer:**

| Arquivo | O que controla |
|---|---|
| `static/js/tailwind-config.js` | Paleta e fontes — fonte única para loja, admin e vendedor |
| `static/css/livro.css` | Textura, capas, capitular, ornamentos, animações |
| `templates/partials/head.html` | Fontes, Tailwind, favicon (incluído em todas as páginas) |
| `templates/partials/capa.html` | Capa do livro (foto ou capa tipográfica) |
| `templates/layout.html` | Cabeçalho e rodapé da loja |

---

## Requisitos

**Com Docker (recomendado):** apenas Docker e Docker Compose.

**Sem Docker:** Go 1.25 ou superior. Nada além disso — o driver SQLite é Go puro, então
**não é necessário instalar GCC/TDM-GCC**, ao contrário do que versões antigas deste projeto pediam.

---

## Início rápido (Docker)

### 1. Crie o arquivo `.env`

```bash
cp .env.example .env
```

Gere os segredos e cole no `.env`:

```bash
# SESSION_SECRET (mínimo 32 caracteres)
openssl rand -base64 48

# SETTINGS_ENCRYPTION_KEY (exatamente 32 bytes em Base64)
openssl rand -base64 32
```

Defina também `ADMIN_USERNAME` e `ADMIN_PASSWORD` (mínimo 12 caracteres). Esse será o primeiro
administrador, criado apenas se ainda não existir nenhum.

> **Não existem mais contas padrão.** As antigas `admin/admin123` e `vendedor/vendedor123` foram
> removidas do código e são apagadas do banco na migração.

### 2. Suba a loja

```bash
docker compose up -d --build
```

Acesse:

| | Endereço |
|---|---|
| Loja | http://localhost:8080 |
| Admin | http://localhost:8080/admin/login |
| Vendedor | http://localhost:8080/vendedor/login |
| Saúde | http://localhost:8080/healthz |

### 3. Comandos do dia a dia

```bash
docker compose logs -f loja      # acompanhar os logs
docker compose restart loja      # reiniciar
docker compose down              # parar (os dados ficam nos volumes)
docker compose run --rm backup   # gerar um backup em ./backups
```

---

## Rodando sem Docker

```bash
go mod download

# Linux/macOS
export SESSION_SECRET="$(openssl rand -base64 48)"
export SETTINGS_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export ADMIN_USERNAME="admin"
export ADMIN_PASSWORD="uma-senha-bem-longa"
go run .
```

```powershell
# Windows (PowerShell)
$env:SESSION_SECRET = "coloque-aqui-32-caracteres-ou-mais"
$env:SETTINGS_ENCRYPTION_KEY = "chave-base64-de-32-bytes"
$env:ADMIN_USERNAME = "admin"
$env:ADMIN_PASSWORD = "uma-senha-bem-longa"
go run .
```

Sem `APP_ENV=production`, o `SESSION_SECRET` é opcional: a aplicação gera um segredo temporário
para desenvolvimento e avisa no log (as sessões caem a cada reinício).

---

## Variáveis de ambiente

| Variável | Obrigatória | Padrão | Descrição |
|---|---|---|---|
| `SESSION_SECRET` | em produção | — | Assina os cookies de sessão. Mínimo 32 caracteres. |
| `SETTINGS_ENCRYPTION_KEY` | sim, para salvar segredos | — | Chave AES-256 (Base64 de 32 bytes) que criptografa os segredos no banco. |
| `ADMIN_USERNAME` | no 1º start | — | Usuário do primeiro administrador. |
| `ADMIN_PASSWORD` | no 1º start | — | Senha do primeiro administrador (mínimo 12 caracteres). |
| `MERCADOPAGO_ACCESS_TOKEN` | não | — | Token de produção. Tem precedência sobre o valor salvo no painel. |
| `MERCADOPAGO_WEBHOOK_SECRET` | para receber pagamentos | — | Chave secreta da notificação. **Sem ela, nenhum pedido é confirmado automaticamente.** |
| `WHATSAPP_API_KEY` | não | — | API key do CallMeBot para avisar sobre pedidos pagos. |
| `GOOGLE_CLIENT_SECRET` | para login com Google | — | Chave secreta do cliente OAuth do Google. Tem precedência sobre o valor salvo no painel. |
| `MELHORENVIO_CLIENT_SECRET` | para frete | — | Chave secreta do aplicativo da loja no Melhor Envio. Tem precedência sobre o painel. |
| `MELHORENVIO_API_URL` | não | — | Substitui o endereço da API do Melhor Envio (homologação e testes). |
| `DB_PATH` | não | `./data/loja.db` | Caminho do banco. No Docker aponta para o volume. |
| `PORT` | não | `8080` | Porta do servidor. |
| `APP_ENV` | não | — | `production` liga HSTS, cookie `Secure` e exige `SESSION_SECRET`. |
| `TRUSTED_PROXY` | não | `false` | `true` **somente** se houver proxy reverso sanitizando `X-Forwarded-For`. O `docker-compose.prod.yml` já liga. |
| `SEED_DEMO` | não | fora de produção | `true` cria livros de exemplo numa loja vazia; em produção fica desligado. |

> ⚠️ **`TRUSTED_PROXY=true` sem proxy na frente é uma falha de segurança:** o cliente passa a poder
> forjar o próprio IP e escapar do limite de tentativas de login.

---

## Configurando o Mercado Pago

1. Crie a conta em [mercadopago.com.br](https://www.mercadopago.com.br) e cadastre CPF/CNPJ e dados bancários.
2. Cadastre sua **chave PIX** em *Seu negócio → Configurações*.
3. Em *Suas integrações → Criar aplicação*, abra as **Credenciais de produção** e copie o **Access Token**.
4. Ainda na aplicação, vá em **Webhooks → Configurar notificações**:
   - URL: `https://seudominio.com.br/webhook/mp`
   - Eventos: **Pagamentos**
   - Copie a **chave secreta** que aparece ali.
5. No painel da loja, em `/admin/configuracoes`, preencha o Access Token, a chave secreta do webhook,
   a **URL base da loja** e o nome da loja. Em produção, prefira as variáveis de ambiente.

### Parcelamento

O cartão é pago na página segura do Mercado Pago (Checkout Pro), com **até 12x**. Antes de sair do site,
o cliente já vê a maior parcela disponível, na página do livro e no checkout ("ou 12x de R$ 8,13 no
cartão"). O valor vem da própria API do Mercado Pago, com as taxas da conta que recebe, e fica em cache
por 1 hora. Por isso aparece **"com juros"** quando o comprador paga os juros, que é o padrão, e **"sem
juros"** se a conta tiver parcelamento sem juros ativado. Valores baixos oferecem menos parcelas: o
Mercado Pago exige um valor mínimo por parcela.

Quem tem conta no Mercado Pago pode pagar com o **cartão salvo lá**. O cartão fica guardado no Mercado
Pago, nunca neste site, o que atende a LGPD e as regras de segurança de cartão (PCI).

### Comprovante de compra

Depois do pagamento confirmado, a tela do pedido oferece **Baixar PDF** e **Enviar para meu e-mail**, e o
comprovante também é enviado sozinho no momento da confirmação.

- **Não é nota fiscal.** NF-e/NFS-e exige empresa (CNPJ), certificado digital e um emissor (Focus NFe,
  NFE.io, eNotas etc.). O documento diz isso de forma explícita.
- Só quem fez o pedido baixa o PDF, só depois do pagamento, e sem cache (tem dados pessoais).
- O e-mail vai **sempre para o endereço do pedido**, nunca para um digitado na hora, com no máximo 3
  envios por pedido por hora.
- **Configuração:** em Configurações → E-mail, informe a conta Gmail e uma **senha de app** (exige
  verificação em duas etapas; use uma conta só da loja). O envio exige TLS e é recusado se o servidor não
  oferecer criptografia.

### Vendedores recebendo direto: divisão automática (split)

Livro do próprio site: **100% do valor na conta do site**. Livro de vendedor: o valor cai **na conta
Mercado Pago do vendedor**, e o Mercado Pago retém a **comissão do site** (padrão de 10%, ajustável em
Configurações, de 0 a 50%) e a manda para a conta do site. O dinheiro do vendedor nunca passa pela conta do
site. Se passasse, o site estaria guardando valores de terceiros, atividade regulada pelo Banco Central.

**Como ativar:**
1. Na sua aplicação do Mercado Pago, copie **Client ID** e **Client Secret** (credenciais de produção) para
   Configurações, ou para `MERCADOPAGO_CLIENT_ID` e `MERCADOPAGO_CLIENT_SECRET`.
2. Cadastre na aplicação a URL de redirecionamento `https://SEU-DOMINIO/vendedor/mercadopago/retorno`.
   Ela precisa ser **https** e idêntica à mostrada em Configurações (que vem da URL Base da Loja).
3. Cada vendedor clica em **Conectar Mercado Pago** no painel dele e autoriza o site.
4. Cada vendedor precisa ter uma **chave PIX cadastrada na própria conta do Mercado Pago**.

**Regras:**
- Livros de vendedor **sem conta conectada não aparecem na loja**: não haveria para onde mandar o dinheiro.
- A sacola aceita livros de **um vendedor por vez** (ou só do site), porque cada pagamento tem um único
  recebedor. O pedido confere isso de novo no servidor.
- A conta Mercado Pago do próprio site não pode ser conectada como vendedor.

**Segurança:**
- Os tokens dos vendedores dão acesso às contas deles. Ficam criptografados com `SETTINGS_ENCRYPTION_KEY`
  e nunca aparecem em tela. Um `state` aleatório amarra o retorno do OAuth à sessão de quem começou, o que
  impede alguém de conectar a própria conta no lugar do vendedor.
- Antes de liberar um pedido, a confirmação confere que o pagamento foi **recebido na conta registrada
  para aquele pedido** e que **a comissão do site foi retida**. Sem isso, um vendedor poderia criar na
  conta dele um pagamento apontando para o pedido de outro e marcá-lo como pago.
- Tokens de vendedor são renovados automaticamente 15 dias antes de vencer (valem 180 dias).

### Como o pagamento funciona

| Meio | Fluxo |
|---|---|
| **PIX** | A loja cria a cobrança e mostra o QR Code em `/pix?pedido=N`. A página consulta o status a cada 4s. O QR Code expira em 30 minutos. |
| **Cartão / boleto** | A loja cria uma preferência do Checkout Pro e redireciona o cliente ao Mercado Pago, que devolve para `/pagamento/retorno`. Parcelamento em até 12x. |

**A loja nunca recebe nem armazena dados de cartão** — eles são digitados no ambiente do Mercado Pago.

A confirmação de pagamento tem três travas:

1. O webhook só é aceito com **assinatura HMAC válida** (cabeçalho `x-signature`), dentro de uma
   janela de 10 minutos.
2. O status e o valor **nunca vêm do corpo do webhook**: a aplicação consulta a API do Mercado Pago.
3. Um pagamento aprovado **por valor menor que o pedido não libera nada** — fica registrado em log
   como alerta para conferência manual.

> Para testar o webhook em desenvolvimento local, use um túnel como [ngrok](https://ngrok.com) e
> informe a URL pública em *URL Base da Loja*.

---

## Frete e etiquetas (Melhor Envio)

O envio dos livros físicos é feito **pelo vendedor** (ou pelo próprio site, nos livros que ele
vende), e isso aparece na página do livro, na sacola, no checkout e no pedido.

**Como funciona para o comprador:** na **página do livro** há "Calcular frete e prazo": ele digita o
CEP e vê as opções com preço e prazo antes de colocar na sacola. No checkout ele informa o CEP; o endereço se preenche sozinho
(ViaCEP) e aparecem as opções dos Correios (PAC, SEDEX, Mini Envios) com preço e prazo, cotadas no
Melhor Envio a partir do endereço de despacho do vendedor e do peso/medidas dos livros. O frete
escolhido entra no total. O CPF é pedido porque vai na etiqueta (declaração de conteúdo dos
Correios); ele não aparece para o vendedor. Depois do envio, o código de rastreio aparece na página
do pedido com link para acompanhar.

**Dinheiro:** o frete vai para quem envia, junto com o valor dos livros, no split do Mercado Pago. A
comissão do site incide **só sobre os livros**. O preço do frete é cotado de novo no servidor ao
fechar o pedido, então não dá para adulterá-lo no navegador.

**Como funciona para o vendedor:** em **Envio** (painel), ele cadastra de onde despacha e conecta a
própria conta Melhor Envio (OAuth). No pedido pago aparece **Gerar etiqueta**: o site coloca a
etiqueta no carrinho dele no Melhor Envio, paga com o **saldo da conta dele**, gera e mostra o botão
**Imprimir etiqueta**, além de gravar o rastreio. Se faltar saldo, a etiqueta fica no carrinho do
Melhor Envio e a mensagem explica o que fazer. Quem tem CNPJ pode informar a chave da NF-e; sem ela,
o envio vai com declaração de conteúdo. Quem não conectar a conta continua vendendo: o frete é
cotado com a conta do site e o vendedor informa o rastreio à mão.

Livro físico ganhou **peso e medidas** no cadastro, já preenchidos com um pacote padrão de livro
(400 g, 4 × 16 × 23 cm). Livros antigos usam esse padrão.

**Configurar (uma vez):**

1. Crie uma conta no [Melhor Envio](https://melhorenvio.com.br) (para testar, use o
   [sandbox](https://sandbox.melhorenvio.com.br) e marque "Ambiente de testes").
2. Em **Integrações → Área Dev**, crie um aplicativo com a URL de redirecionamento
   `https://livredo.com.br/melhorenvio/retorno`.
3. Em **Admin → Configurações → Frete e etiquetas**, cole Client ID, Client Secret e um e-mail de
   contato técnico (o Melhor Envio exige).
4. Em **Admin → Envio**, cadastre o endereço de despacho do site e conecte a conta da loja.

Sem nada configurado, a loja continua funcionando: o frete fica "combinado com o vendedor".

> A integração foi testada contra uma simulação da API do Melhor Envio. Antes de liberar para os
> vendedores, faça uma compra completa no **sandbox** (cotação, pagamento e etiqueta).

---

## Login com Google (compradores)

O login e o cadastro de clientes ganham o botão **Continuar com o Google**. Vale só para
compradores; vendedores e admin continuam com usuário e senha.

**Configurar (uma vez):**

1. Em [console.cloud.google.com](https://console.cloud.google.com), crie um projeto.
2. Em **APIs e serviços → Tela de consentimento OAuth**, escolha **Externo** e preencha nome,
   e-mail de suporte e logotipo. Escopos: só `openid`, `email` e `profile`.
3. Em **Credenciais → Criar credenciais → ID do cliente OAuth**, tipo **Aplicativo da Web**, e
   cadastre a URI de redirecionamento `https://SEU-DOMINIO/conta/google/retorno`.
4. Em **Admin → Configurações → Login com Google**, cole o **Client ID** e o **Client Secret**
   (ou use `GOOGLE_CLIENT_SECRET`). A *URL Base da Loja* precisa estar preenchida.
5. Enquanto o app estiver em modo **Teste** no Google, só os e-mails cadastrados como testadores
   entram. Publique o app para liberar a todos.

**Como funciona:**

- Fluxo OpenID Connect com código + PKCE, `state` (amarra a volta à sessão que começou o login) e
  `nonce` (amarra o ID token a esse login). O site guarda só nome, e-mail e o identificador da conta
  Google.
- **Primeiro login** com um e-mail novo cria a conta, sem senha.
- **E-mail que já tinha cadastro com senha**: a conta é vinculada ao Google e **a senha antiga é
  desativada**. O cadastro por senha não confirma o e-mail, então alguém poderia ter criado antes
  uma conta com o e-mail de outra pessoa; quem entra pelo Google provou ser o dono do e-mail.
- E-mail não verificado pelo Google é recusado, e um e-mail já ligado a uma conta Google não pode
  ser assumido por outra.

---

## Banco de dados

SQLite em arquivo único, guardado em um **volume Docker nomeado** (`loja-data`), fora da imagem.
Recriar o container não apaga nada.

Configuração aplicada na conexão:

| PRAGMA | Valor | Por quê |
|---|---|---|
| `journal_mode` | `WAL` | Leituras não travam durante uma escrita |
| `foreign_keys` | `1` | Integridade referencial de fato aplicada |
| `busy_timeout` | `5000` | Espera 5s em vez de falhar com *database is locked* |
| `synchronous` | `NORMAL` | Seguro com WAL e muito mais rápido que `FULL` |

A aplicação **verifica esses PRAGMAs no start e encerra se não estiverem ativos** — é melhor falhar
na subida do que descobrir em produção que as chaves estrangeiras nunca funcionaram.

> **Nota histórica:** a conexão usava `?_journal_mode=WAL&_foreign_keys=on`, que é a sintaxe do driver
> `mattn/go-sqlite3`. O driver deste projeto é o `modernc.org/sqlite`, que ignorava esses parâmetros
> em silêncio. O banco rodava sem WAL, **sem chaves estrangeiras** e sem tempo de espera. Agora usa
> `_pragma=nome(valor)`, que é o formato correto.

O arquivo é gravado com permissão `0600` e pertence a um usuário sem privilégios.

### Onde hospedar

A recomendação para esta loja é **manter o SQLite no volume Docker**, que é o que está configurado:
sem custo, sem latência de rede e com backup simples. SQLite aguenta com folga o volume de uma loja
pequena ou média — o gargalo aparece só com escritas concorrentes muito intensas.

Migrar para PostgreSQL gerenciado (Neon, Supabase) passa a fazer sentido quando você precisar rodar
**mais de uma instância da aplicação ao mesmo tempo**, já que o arquivo SQLite não é compartilhável
entre servidores.

---

## Backup e restauração

### Gerar um backup

```bash
docker compose run --rm backup
```

Gera `./backups/loja-AAAAMMDD-HHMMSS.db` e copia para `./backups/ebooks` e `./backups/fotos` os
arquivos de e-book e as fotos enviadas que ainda não estavam lá (os nomes são únicos e os arquivos
nunca são reescritos, então só o que é novo é copiado).

O backup roda `VACUUM INTO` dentro da própria aplicação, o que produz um arquivo íntegro **com o
servidor no ar**. Copiar o `loja.db` por fora **não funciona**: com WAL, as gravações recentes ficam
no arquivo `loja.db-wal` e a cópia sai incompleta.

### Agendar backup diário (Linux)

```cron
0 3 * * * cd /caminho/do/projeto && docker compose run --rm backup
```

Guarde uma cópia **fora do servidor** (S3, Backblaze, Google Drive). Backup no mesmo disco não
protege contra perda do disco.

### Restaurar

```bash
docker compose down
docker run --rm -v loja-data:/data -v "$PWD/backups":/backups alpine \
  sh -c "cp /backups/loja-AAAAMMDD-HHMMSS.db /data/loja.db && rm -f /data/loja.db-wal /data/loja.db-shm"
docker compose up -d
```

---

## Painéis

### Administrador — `/admin`

- **Dashboard** — total de pedidos, receita e produtos
- **Produtos** — cadastro completo, com upload e recorte de imagem, e vínculo a um vendedor
- **Pedidos** — listagem e alteração de status
- **Categorias** — a lista que alimenta os formulários de produto do admin e do vendedor
- **Vendedores** — lista de quem já se cadastrou; o admin também pode criar contas manualmente
- **Configurações** — Mercado Pago e WhatsApp

### Vendedor — `/vendedor`

Reformulado para ser operacional, não técnico:

- **Cadastro próprio** (`/vendedor/cadastro`) — o vendedor cria a própria conta, sem depender do
  admin, informando nome, usuário, **e-mail** e **CPF ou CNPJ**. A senha (mínimo 12 caracteres) é o
  controle de acesso; a conta já nasce ativa. E-mail e documento são únicos por conta — cada CPF/CNPJ
  só pode estar vinculado a um vendedor, e o CPF/CNPJ é validado pelo dígito verificador oficial
  (rejeita, por exemplo, `111.111.111-11`, que passaria no cálculo ingênuo mas nunca foi emitido).
- **Início** — vendas confirmadas, pedidos a enviar e **alerta de estoque baixo** (≤ 5 unidades).
  A receita conta apenas pedidos efetivamente pagos.
- **Meus produtos** — lista com miniatura, com destaque para estoque baixo
- **Novo/editar produto** — **upload de foto** (JPEG, PNG, WebP ou GIF até 4 MB), **categoria em
  lista fechada** e **estado do livro (novo ou usado)**, obrigatório. Produtos cadastrados antes desse
  campo aparecem como "estado não informado" até serem editados; a loja não presume "novo". O campo "URL da imagem" foi removido: o vendedor não precisa saber o que é uma URL.
  O preço aceita vírgula (`199,90`) ou ponto (`199.90`).
- **Meus pedidos** — apenas os itens dele, com o subtotal correspondente

### Quem vende cada livro

Todo livro mostra quem o vende, nos cartões da vitrine, na página do livro e na sacola:

| Cadastrado por | Aparece como |
|---|---|
| Admin (sem vendedor) | **Vendido pelo Livredo** |
| Vendedor com CNPJ | Nome do vendedor · **Loja** |
| Vendedor com CPF | Nome do vendedor · **Pessoa física** |

O tipo vem do documento informado no cadastro. O CPF/CNPJ em si nunca é exibido nem enviado ao
navegador do comprador: o banco devolve só o tipo.

O vendedor **não confirma pagamento** — isso é exclusivo do webhook. Ele só avança
`pago → enviado → entregue`.

### Foto de perfil

Clientes (em **Minha conta**) e vendedores (no **Início** do painel) podem enviar uma foto de
perfil: JPEG, PNG, WebP ou GIF de até 4 MB, com o tipo conferido pelo conteúdo do arquivo, não
pela extensão. Sem foto, aparece a inicial do nome. Ao trocar ou remover, o arquivo antigo é apagado.

### Reputação do vendedor (estrelas e níveis)

Só o vendedor tem reputação. Quem avalia é o **comprador**, uma vez por pedido, e só depois que o
pedido foi **enviado** ou **entregue** — o formulário aparece na página do pedido. Vendas do próprio
site não entram.

O nível depende **só da média**: cada avaliação ruim a puxa para baixo, então quem acumula
reclamações não sobe. Sem nenhuma avaliação, o vendedor fica no Cinza.

| Nível | Média mínima | Desconto na comissão (padrão) |
|---|---|---|
| Cinza | — (sem avaliações ou abaixo de 3,0) | — |
| Bronze | 3,0 | — |
| Prata | 3,8 | — |
| Ouro | 4,4 | −2 pontos (10% vira 8%) |
| **Esmeralda** (máximo) | 4,8 | −4 pontos (10% vira 6%) |

**Desconto na comissão.** Vendedores Ouro e Esmeralda pagam menos comissão ao site. Os descontos, em
pontos percentuais, ficam em **Admin → Configurações → Vendedores** (um campo por nível). Como o
desconto é dinheiro, ele só vale a partir de **10 vendas avaliadas** (`MinRatedSalesForDiscount`, em
[models/commission.go](models/commission.go)): sem esse mínimo, uma única nota 5 dada por um amigo já
reduziria a comissão de todas as vendas. A comissão é fixada no pedido (e retida pelo Mercado Pago)
no momento da compra; o painel do vendedor mostra a comissão atual e quantas avaliações faltam.

Selo, estrelas (com meia estrela) e média aparecem na página do livro, na página do pedido e no
painel do vendedor. As médias de cada nível ficam em `sellerTiers`, em [models/review.go](models/review.go).

### Denúncias

Na página do livro e na página do pedido há **Denunciar vendedor**. Só clientes logados denunciam,
com motivo (golpe/fraude, violação de direitos autorais, não entregue, produto diferente, pagamento
fora do site, comportamento abusivo, outro) e descrição de 20 a 2000 caracteres; o pedido envolvido
é opcional, mas precisa ser do próprio cliente com aquele vendedor. Denúncia feita a partir da
página de um livro já aponta o livro. Limite de 5 denúncias por cliente a cada 24 h.

O vendedor **nunca** vê quem denunciou. Em **Admin → Denúncias** (com contador de abertas no menu)
o admin vê o denunciante, muda a situação (aberta, em análise, resolvida, descartada), deixa uma
anotação interna e pode **suspender** o vendedor: o painel dele é bloqueado e os livros saem da
loja na hora. **Reativar** devolve tudo. Quando a denúncia aponta um livro, há também **Tirar
livro da loja**, que o esconde na hora sem apagar nada; quem já comprou continua podendo baixar.

### Livros digitais (e-books)

Vendedores e o admin cadastram **e-books** além de livros físicos. A ideia é abrir espaço para
autores independentes e novos escritores, para quem publicar em digital é o caminho mais acessível.

**Cadastro** (painel do vendedor ou do admin → Novo livro → Formato: *E-book*):

- Arquivo **PDF ou EPUB** de até 60 MB. O tipo é conferido pelo conteúdo do arquivo, não pela
  extensão: um texto ou executável renomeado para `.pdf` é recusado.
- **Declaração de direitos obrigatória** a cada arquivo enviado ("sou o autor ou tenho autorização").
  A data fica gravada em `products.rights_declared_at`.
- Campo **Autor(a)**, que aparece na página do livro, nos cartões da vitrine e na capa gerada.
- Sem estoque e sem estado (novo/usado). O formato é escolhido só no cadastro e não muda depois,
  para um e-book vendido não virar livro físico e tirar o download de quem comprou.
- **Só vende e-book quem a loja aprovar** (contra pirataria). No painel, em **Livros digitais**, o
  vendedor pede autorização como **autor(a) independente** (informando os nomes com que publica) ou
  **editora/distribuidora parceira**, com a comprovação (ISBN, registro na Biblioteca Nacional,
  perfil de autor, contrato). O admin analisa em **Autores e editoras** (contador no menu) e aprova,
  corrige os nomes, recusa ou revoga, sempre com uma observação para o vendedor.
  - Sem aprovação, a opção "E-book" fica bloqueada no cadastro, e o modelo recusa até um POST forjado.
  - **Autor(a)** só cadastra e-books cujo autor seja um dos nomes aprovados: quem publica como "Ana
    Souza" não consegue subir uma obra de "Mauricio de Sousa".
  - **Editora parceira** publica qualquer autor (os direitos foram conferidos na aprovação).
  - **O mesmo arquivo não pode estar à venda duas vezes**: o SHA-256 de cada arquivo fica gravado, o
    que barra o PDF pirata repostado por outro vendedor.
  - **Revogar** tira da vitrine e da sacola, na hora, todos os e-books do vendedor (quem já comprou
    continua baixando).
  - A página do e-book mostra o selo **Publicado pelo autor** ou **Editora parceira**.
  - A aprovação é a trava principal: confira a comprovação com cuidado, porque o hash só barra
    arquivos repetidos, não a primeira cópia pirata de um livro.
  - A loja (livros do próprio site) não precisa de aprovação.
- Denúncias com motivo "violação de direitos autorais" e o botão **Tirar livro da loja** continuam
  valendo para qualquer livro.
- Só livros pagos: não há e-book gratuito.

**Compra**:

- Prateleira **E-books** no menu do topo e no filtro da vitrine (`/?estado=digital`).
- E-book exige **conta**: o livro fica na biblioteca do cliente. Quem não está logado é levado ao
  login (ou ao Google) e volta direto ao checkout. O endereço de volta só aceita caminhos do próprio
  site, para o link de login não virar porta para página falsa.
- Uma cópia por cliente: a quantidade é sempre 1 e quem já tem o livro não consegue comprá-lo de novo.
- Pedido só de e-books **não pede endereço** e vai direto para *Entregue* quando o pagamento é
  confirmado. Pedido misto (físico + digital) pede endereço, fica *Pago* até o envio, e o e-book
  já é liberado.
- A comissão do site e a divisão com o vendedor são as mesmas do livro físico.

**Entrega**:

- **Minha biblioteca** (`/conta/biblioteca`) lista os e-books comprados, com botão de download. O
  botão também aparece na página do pedido e na página do livro. O e-mail de confirmação leva o
  link da biblioteca.
- O arquivo fica na pasta `ebooks/`, ao lado do banco (`DB_PATH`; no Docker, dentro do volume do banco), **fora** de
  `static/`: não existe URL pública para ele. O único caminho é `/conta/biblioteca/baixar`, que
  confere se o cliente tem pedido pago com o livro.
- Download com até 30 arquivos por cliente por hora, retomável (Range) e sem cache.
- Se o autor enviar uma versão corrigida, quem já comprou passa a baixar a nova. Livro tirado da
  loja ou arquivado continua disponível para quem comprou.

Ainda não há marca d'água com o nome do comprador nem DRM: um arquivo baixado pode ser repassado.

---

## Rotas

### Loja

| Rota | Método | Descrição |
|---|---|---|
| `/` | GET | Vitrine, com busca (`q`), gênero (`categoria`) e estado (`estado=novo` ou `estado=usado`), combináveis |
| `/produto?id=N` | GET | Detalhes do produto |
| `/carrinho` | GET | Carrinho |
| `/carrinho/add`, `/update`, `/remove` | POST | Operações do carrinho |
| `/checkout` | GET/POST | Dados do cliente e escolha do pagamento |
| `/pedido?id=N` | GET | Confirmação do pedido |
| `/healthz` | GET | Saúde do serviço |

### Cliente

| Rota | Método | Descrição |
|---|---|---|
| `/conta/cadastro`, `/conta/entrar` | GET/POST | Cadastro e login |
| `/conta/sair` | POST | Logout |
| `/conta` | GET/POST | Dados da conta |
| `/conta/pedidos` | GET | Histórico de pedidos |
| `/conta/foto` | POST | Envia ou remove (`acao=remover`) a foto de perfil |
| `/conta/google` | GET | Inicia o login com Google |
| `/conta/google/retorno` | GET | Volta do login com Google |
| `/conta/biblioteca` | GET | E-books comprados |
| `/frete/cotar?cep=N[&livro=N]` | GET | Endereço do CEP e frete: de um livro (`livro=`) ou da sacola (JSON) |
| `/conta/biblioteca/baixar?livro=N` | GET | Download do e-book (só para quem comprou) |
| `/pedido/avaliar` | POST | Avalia o vendedor de um pedido enviado (1 a 5 estrelas) |
| `/denunciar?vendedor=N[&pedido=N]` | GET/POST | Denúncia contra um vendedor |

### Pagamento

| Rota | Método | Descrição |
|---|---|---|
| `/pix?pedido=N` | GET | QR Code e copia-e-cola |
| `/pix/status?pedido=N` | GET | Status do pedido em JSON |
| `/pagamento/retorno?pedido=N` | GET | Retorno do Checkout Pro |
| `/webhook/mp` | POST | Webhook do Mercado Pago (assinatura HMAC obrigatória) |

### Admin — todas exigem sessão de administrador

`/admin`, `/admin/produtos[/novo|/editar|/excluir]`, `/admin/pedidos[/detalhe]`,
`/admin/categorias`, `/admin/vendedores`, `/admin/ebooks`, `/admin/denuncias`, `/admin/envio`, `/admin/configuracoes`, `/admin/login`, `/admin/logout`

### Vendedor

`/vendedor/cadastro`, `/vendedor/login`, `/vendedor/logout` são públicas; as demais exigem sessão
de vendedor: `/vendedor`, `/vendedor/foto`, `/vendedor/ebooks`, `/vendedor/envio`, `/vendedor/produtos[/novo|/editar|/excluir]`, `/vendedor/pedidos[/detalhe]`.

`/melhorenvio/conectar`, `/melhorenvio/retorno` e `/melhorenvio/desconectar` servem ao vendedor e
ao admin (`?para=admin`); o handler confere quem está logado na ida e na volta.

---

## Segurança

| Proteção | Como |
|---|---|
| Senhas | bcrypt com custo 12; mínimo de 12 caracteres |
| Sessão | Cookie assinado, `HttpOnly`, `SameSite=Lax`, `Secure` em produção |
| CSRF | Token por sessão em todo POST/PUT/PATCH/DELETE, comparado em tempo constante |
| Rotação de token | Token novo a cada login e logout |
| Força bruta no login | 5 tentativas por IP a cada 15 min, com bloqueio de 15 min, por painel |
| Cadastro em massa de vendedor | 10 tentativas por IP a cada 30 min, sem reset em caso de sucesso |
| Identidade do vendedor | E-mail e CPF/CNPJ obrigatórios e únicos por conta; documento validado pelo dígito verificador oficial |
| Webhook | Assinatura HMAC-SHA256 obrigatória, com janela de 10 min contra reenvio |
| Valor do pagamento | Pagamento menor que o pedido não libera a entrega |
| Confirmação duplicada | `MarkOrderPaid` é idempotente: webhook repetido não renotifica |
| Preço do pedido | Vem sempre do banco, nunca do carrinho enviado pelo cliente |
| Estoque | Baixa transacional com `UPDATE ... WHERE stock >= ?` |
| LGPD: termos e privacidade | `/termos` e `/privacidade` (templates `terms.html` e `privacy.html`), com controlador, documento e encarregado (DPO) preenchidos em Configurações → LGPD. Aceite obrigatório no cadastro de cliente e de vendedor e no checkout; contas criadas pelo Google e contas antigas aceitam em `/termos/aceitar` antes de usar as áreas logadas. Cada aceite fica em `consents` (versão, data, IP, navegador, origem) e nunca é apagado; o pedido guarda `terms_version`. **Ao mudar o texto, troque `models.TermsVersion`**: todos aceitam de novo. Direitos do titular: cliente baixa os dados (JSON) e exclui a conta em `/conta/privacidade` (nome, e-mail, telefone, endereço, foto e senha apagados; pedidos pagos guardados sem vínculo por obrigação legal); vendedor baixa os dados em `/vendedor/meus-dados.json` |
| Importação por planilha | Painel do vendedor e do admin → Livros → **Importar planilha** (`/vendedor/importar`, `/admin/importar`). Modelo CSV em `…/importar/modelo.csv` (`;`, UTF-8 com BOM; aceita também `,`, tabulação e Windows-1252 do Excel). Até 1.000 livros e 2 MB. O processamento roda em segundo plano: busca os ISBNs, valida cada linha (título, categoria cadastrada, estado, preço, ISBN com dígito verificador, repetidos) e mostra uma **prévia**; nada é publicado sem confirmar. Linha com ISBN (ou `id`) de um livro que o lojista já tem atualiza só preço e estoque. Capas: coluna `capa_url` ou a capa do ISBN, baixadas no servidor só de endereços públicos (bloqueia localhost e rede interna). Só livros físicos |
| Busca por ISBN | Google Books (título, autor, editora, idioma, sinopse) + Open Library (capa e o que faltar), com cache em `isbn_cache`. Também no formulário de livro (botão **Buscar dados**). A chave do Google Books fica em Configurações (ou `GOOGLE_BOOKS_API_KEY`); sem ela, a cota anônima do Google costuma estar esgotada e só o Open Library responde |
| Edição em massa e exportação | **Edição em massa** (`…/produtos/massa`): preço, estoque e à venda/pausado direto na tabela, filtro, e ações nos marcados (ajustar preço em %, definir ou somar estoque, colocar à venda, pausar); grava tudo ou nada. **Exportar** (`…/produtos/exportar.csv`): o catálogo no formato da importação, com `id`, para alterar e reenviar. No admin, valem para os livros do próprio Livredo |
| Prazo de despacho | Ao confirmar o pagamento de livro físico, o pedido ganha `ship_deadline`: 48 h úteis para editoras, sebos e livrarias e 72 h para autores e leitores (Configurações → Prazo de postagem), sem fins de semana, feriados nacionais, Carnaval e Sexta-feira Santa (`models/dispatch.go`). O vendedor marca **Pronto para envio** e **Postado** (com rastreio); o comprador recebe e-mail a cada passo e na entrega, com textos editáveis e marcadores (`{nome}`, `{pedido}`, `{rastreio}`…). Rotina a cada 30 min: lembrete ao vendedor 12 h antes do prazo, alerta de atraso, rastreio das etiquetas do Melhor Envio (postado e entregue automáticos) e pausa automática. Últimos 60 dias: 80% postados no mesmo dia útil (mín. 3) = medalha **Envio Rápido**; 25% de atrasos (mín. 4) ou pedido atrasado = exposição reduzida; atraso de mais de 3 dias úteis ou 50% de atrasos (mín. 6) = loja pausada (livros fora da vitrine, conta ativa) até o admin reativar no painel, o que zera o histórico |
| Ordem da vitrine | `models/ranking.go`: livros de vendedores com nível mais alto aparecem primeiro (Esmeralda 5 … sem avaliações 1; o próprio Livredo vale 3), +1 com Envio Rápido, −3 com exposição reduzida; empates mantêm a ordem de cadastro |
| Audiobook | Em cada e-book, o vendedor (com venda de e-books aprovada) envia faixas MP3 ou M4A/M4B de até 300 MB (2 GB e 200 faixas por livro) em `…/produtos/audio?id=N`, declara os direitos de áudio e narração e define o preço do audiobook e, se quiser, do combo e-book + audiobook. Na página do livro o comprador escolhe a edição (e-book, audiobook ou combo); o preço vem sempre do banco e `order_items.edition` registra o que foi comprado (pedidos antigos valem como e-book). Quem tem o audiobook ouve em `/conta/biblioteca/ouvir` (player por faixa, velocidade, ±30 s, lembra onde parou) e baixa por faixa ou tudo em ZIP. Se a 1ª faixa for MP3, os primeiros 5 minutos são amostra grátis (`/livro/amostra/audio`). Arquivos em `DATA_DIR/audiobooks`, fora de `static/`; o mesmo áudio não pode estar à venda em dois livros. O Caddy aceita até 305 MB só nessas rotas |
| Quadrinhos (CBR/CBZ) | Além de PDF e EPUB, o e-book pode ser quadrinho: CBR (RAR) ou CBZ (ZIP) com uma imagem por página, em ordem natural dos nomes. O tipo é detectado pelo conteúdo; pacote sem imagens é recusado. A amostra mostra as páginas escolhidas como imagens (`/livro/amostra/pagina`), só as que estão na amostra |
| Páginas de autor | Todo nome do campo "autor" tem uma página em `/autor/<nome-sem-acentos>` com foto, biografia, site e todos os livros dele à venda, de qualquer vendedor. O nome do autor no livro e no acervo leva sempre a essa página, mesmo quando quem vende é uma editora ou outro detentor dos direitos. Quem escreve a página: o admin (Painel → Páginas de autor) e vendedores com direitos digitais aprovados para aquele autor (autor(a) ou editora parceira); sebos e livreiros sem os direitos não editam |
| Amostra de e-book | A página do livro mostra uma amostra grátis: as 4 primeiras páginas, ou as que o vendedor escolher no formulário ("1-3, 12"), até um quarto do livro e no máximo 10 páginas. Em PDF, o servidor gera um PDF só com essas páginas (`/livro/amostra`, guardado em `DATA_DIR/ebooks/amostras`), exibido com pdf.js servido pelo próprio site (`static/js/pdfjs`); o arquivo completo nunca é enviado. Em EPUB, o texto é dividido em páginas de ~1.800 caracteres e só o texto (sem o HTML do arquivo) aparece na página |
| Cancelamento | Cancelar devolve o estoque dos livros físicos na mesma transação, uma única vez; pedido cancelado não volta para outra situação. Pedido sem pagamento há 72 h é cancelado automaticamente (tarefa a cada hora). Pagamento aprovado para pedido cancelado gera `ALERTA` no log e não reabre o pedido |
| Testar Mercado Pago | Configurações → Mercado Pago → **Testar conexão**: diz se o valor salvo é Access Token de produção, de teste, Public Key ou Client Secret, e mostra a resposta do Mercado Pago (o token nunca é exibido) |
| Acesso a pedido | Vinculado ao cliente logado ou ao cookie de quem comprou |
| Isolamento do vendedor | O `UPDATE` reconfirma o dono; id forjado não alcança produto de terceiro |
| Upload | Tipo detectado pelo **conteúdo**, não pela extensão; nome aleatório; limite de 4 MB |
| Imagem do produto | Restrita a `/static/img/`, sem URL externa nem travessia de caminho |
| Categoria | Validada contra a tabela; POST forjado não inventa categoria |
| Segredos | AES-256-GCM no banco; nunca reexibidos no formulário |
| Cabeçalhos | CSP, HSTS, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Permissions-Policy` |
| Container | Usuário não-root (uid 10001), `read_only`, `no-new-privileges` |

### O que ainda falta para produção séria

- **HTTPS é obrigatório** — coloque um proxy reverso (Caddy, Nginx, Traefik) na frente. O compose
  publica em `127.0.0.1:8080` justamente para a aplicação não ficar exposta direto na internet.
- O limite de tentativas é **por processo e em memória**: reiniciar o container zera a contagem, e
  ele não é compartilhado entre réplicas.
- Não há verificação de e-mail nem recuperação de senha.
- Não há emissão de nota fiscal nem cálculo de frete.

---

## Deploy em produção (livredo.com.br)

A produção roda num servidor Linux (VPS) com dois containers: a loja e o
[Caddy](https://caddyserver.com), que fica na frente com HTTPS automático. Arquivos envolvidos:

| Arquivo | Papel |
|---|---|
| `docker-compose.prod.yml` | Acrescenta o Caddy e liga `TRUSTED_PROXY` |
| `Caddyfile` | HTTPS para `livredo.com.br` e redirecionamento de `www` |
| `deploy/preparar-servidor.sh` | Docker, firewall, atualizações de segurança, swap e backup diário |

### 1. Contratar o servidor

Um VPS com **Ubuntu 24.04**, **2 GB de RAM** (1 GB funciona, com o swap que o script cria) e
datacenter em São Paulo, pela latência. Exemplos: Hostinger VPS, Vultr (São Paulo), Magalu Cloud.
Anote o **IP** do servidor.

### 2. Apontar o domínio (registro.br)

No [registro.br](https://registro.br), abra `livredo.com.br` → **DNS** → editar a zona e crie:

| Tipo | Nome | Valor |
|---|---|---|
| A | *(vazio)* | IP do servidor |
| CNAME | `www` | `livredo.com.br.` |

A propagação costuma levar de minutos a algumas horas. Para conferir: `nslookup livredo.com.br`.

### 3. Preparar o servidor e subir a loja

```bash
ssh root@IP_DO_SERVIDOR
git clone <endereço do repositório> /opt/livredo      # ou copie a pasta do projeto
cd /opt/livredo
sh deploy/preparar-servidor.sh
cp .env.example .env && nano .env
```

No `.env` do servidor, preencha **no mínimo**:

```bash
SESSION_SECRET=         # openssl rand -base64 48
SETTINGS_ENCRYPTION_KEY= # openssl rand -base64 32  (guarde uma cópia fora do servidor!)
ADMIN_USERNAME=admin
ADMIN_PASSWORD=         # 12+ caracteres
COMPOSE_FILE=docker-compose.yml:docker-compose.prod.yml
```

Gere segredos **novos** para produção; não reaproveite os do seu computador. Sem a
`SETTINGS_ENCRYPTION_KEY` original, os segredos salvos no banco não podem ser lidos, então guarde-a
junto com os backups.

```bash
docker compose up -d --build
docker compose logs -f caddy   # espere "certificate obtained successfully"
```

Com o DNS já propagado, `https://livredo.com.br` abre com cadeado. A loja nasce **vazia**: os
livros de exemplo só aparecem fora de produção (ou com `SEED_DEMO=true`).

### 4. Configurar a loja em produção

Em `https://livredo.com.br/admin` → **Configurações**:

1. **URL Base da Loja**: `https://livredo.com.br` (sem `www` e sem barra no fim).
2. **Mercado Pago**: troque para as credenciais de **produção** (`APP_USR-…`); as que começam com
   `TEST-` não cobram de verdade. No painel do Mercado Pago, cadastre:
   - webhook `https://livredo.com.br/webhook/mp` (evento *Pagamentos*) e copie a chave secreta;
   - URL de redirecionamento do OAuth `https://livredo.com.br/vendedor/mercadopago/retorno`.
3. **Login com Google**: URI de redirecionamento `https://livredo.com.br/conta/google/retorno`.
4. **E-mail**: conta Gmail e senha de app, para os comprovantes.

Depois, faça uma compra real de valor baixo com PIX e outra com cartão para validar o caminho
inteiro, inclusive o webhook.

### 5. Atualizar o site depois

No seu computador, na pasta do projeto (Git Bash no Windows):

```bash
sh deploy/publicar.sh
```

O script roda os testes, envia só o código (a lista do Git, respeitando o `.gitignore`) para
`/opt/livredo` no servidor e reconstrói a loja. O `.env`, o banco, as fotos e os certificados ficam
no servidor e não são tocados. Ele usa a chave `~/.ssh/livredo_deploy`; outro servidor ou chave
podem ser passados em `LIVREDO_SERVIDOR` e `LIVREDO_CHAVE`.

Os dados (banco, fotos e certificados) ficam em volumes e sobrevivem às atualizações.

### Backups

O script agenda um backup diário às 3h em `/opt/livredo/backups`, mantendo 14 dias de banco. Os
e-books e as fotos ficam em `backups/ebooks` e `backups/fotos`. Backup só no próprio servidor não
protege contra perda do servidor: copie periodicamente para fora, por exemplo
`scp -r root@IP:/opt/livredo/backups .` no seu computador.

O Caddy aceita envios de até 66 MB só nas rotas de cadastro de livro (por causa do e-book); no resto
do site o limite é 8 MB.

---

## Testes

```bash
go test ./...            # tudo
go test ./... -v         # detalhado
go vet ./...             # análise estática
```

Cobrem, entre outros: assinatura do webhook (incluindo falsificação, troca de identificador e
reenvio), pagamento a menor, idempotência da confirmação, preço vindo do banco, limite de estoque,
limite de tentativas de login, upload com arquivo disfarçado e validação de produto e categoria.

---

## Estrutura do projeto

```
.
├── main.go                     # rotas, servidor e encerramento gracioso
├── database/db.go              # conexão, PRAGMAs, migrações, índices e backup
├── models/
│   ├── admin.go                # autenticação do admin e provisionamento inicial
│   ├── cart.go                 # carrinho em sessão
│   ├── category.go             # categorias curadas pelo admin
│   ├── customer.go             # contas de cliente
│   ├── document.go             # validação de CPF/CNPJ
│   ├── money.go                # formato de moeda brasileiro
│   ├── order.go                # pedidos, estoque e confirmação de pagamento
│   ├── product.go              # produtos e validação
│   ├── secret_settings.go      # criptografia AES-GCM dos segredos
│   ├── seller.go               # contas de vendedor
│   └── settings.go             # leitura/escrita de configurações
├── handlers/
│   ├── admin.go                # painel do administrador
│   ├── customer.go             # área do cliente
│   ├── order_access.go         # quem pode ver qual pedido
│   ├── payment.go              # PIX, Checkout Pro, webhook e /healthz
│   ├── ratelimit.go            # limite de tentativas de login
│   ├── security.go             # CSRF e cabeçalhos de segurança
│   ├── seller.go               # painel do vendedor
│   ├── session.go              # cookie store
│   ├── store.go                # vitrine, carrinho e checkout
│   └── upload.go               # upload de imagem validado
├── services/
│   ├── mercadopago.go          # integração com o Mercado Pago
│   └── whatsapp.go             # notificação via CallMeBot
├── templates/                  # páginas (loja, admin/, seller/)
│   └── partials/               # head da marca e capa do livro, compartilhados
├── static/
│   ├── css/livro.css           # acabamento artístico (capas, ornamentos, textura)
│   ├── js/tailwind-config.js   # paleta e tipografia da marca
│   └── img/                    # favicon, capa padrão e uploads/ (volume)
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

---

## Solução de problemas

| Sintoma | Causa provável e solução |
|---|---|
| `go.mod requires go >= 1.25.0` no build Docker | Imagem do Go mais antiga que o `go.mod`. O Dockerfile atual usa `golang:1.25-alpine`. |
| `SESSION_SECRET é obrigatório em produção` | Falta `SESSION_SECRET` (32+ caracteres) no `.env`. |
| `SETTINGS_ENCRYPTION_KEY deve ser Base64 de 32 bytes` | Gere com `openssl rand -base64 32`. |
| `configure ADMIN_USERNAME e ADMIN_PASSWORD` | Primeiro start sem admin. Senha precisa de 12+ caracteres. |
| `journal_mode=delete; esperado WAL` | Volume montado em sistema de arquivos sem suporte a WAL. Use um volume Docker nomeado. |
| Pedido não vira "pago" | Webhook não cadastrado, `MERCADOPAGO_WEBHOOK_SECRET` ausente/errado ou URL base incorreta. Veja `docker compose logs -f loja`. |
| Webhook responde 503 | `mp_webhook_secret` não configurado — a aplicação recusa por segurança. |
| Webhook responde 401 | Assinatura inválida: a chave secreta não confere com a do painel do Mercado Pago. |
| Imagem some ao recriar o container | Faltou o volume `loja-uploads`. Use o `docker-compose.yml` do projeto. |
| `database is locked` | Concorrência de escrita. O `busy_timeout` de 5s já cobre o caso comum; se persistir, reduza escritas simultâneas. |

---

## Licença

Projeto de uso privado.
