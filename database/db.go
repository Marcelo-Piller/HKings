package database

import (
	"database/sql"
	"log"
	_ "modernc.org/sqlite"
)

var DB *sql.DB

func Init(path string) {
	var err error
	DB, err = sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		log.Fatal("erro ao abrir banco:", err)
	}
	if err = DB.Ping(); err != nil {
		log.Fatal("erro ao conectar banco:", err)
	}
	migrate()
}

func migrate() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			name      TEXT    NOT NULL,
			description TEXT  NOT NULL DEFAULT '',
			price     REAL    NOT NULL,
			stock     INTEGER NOT NULL DEFAULT 0,
			image_url TEXT    NOT NULL DEFAULT '',
			category  TEXT    NOT NULL DEFAULT 'Geral',
			active    INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS orders (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_name TEXT NOT NULL,
			customer_email TEXT NOT NULL,
			customer_phone TEXT NOT NULL DEFAULT '',
			address    TEXT NOT NULL DEFAULT '',
			total      REAL NOT NULL,
			status     TEXT NOT NULL DEFAULT 'pendente',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS order_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id   INTEGER NOT NULL REFERENCES orders(id),
			product_id INTEGER NOT NULL REFERENCES products(id),
			product_name TEXT NOT NULL,
			quantity   INTEGER NOT NULL,
			price      REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admins (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL
		)`,
		`INSERT OR IGNORE INTO admins (username, password) VALUES ('admin', 'admin123')`,
		`INSERT OR IGNORE INTO products (name, description, price, stock, category, image_url) VALUES
			('Camiseta Premium', 'Camiseta 100% algodão de alta qualidade', 79.90, 50, 'Roupas', '/static/img/placeholder.svg'),
			('Tênis Esportivo', 'Tênis ideal para corrida e academia', 249.90, 30, 'Calçados', '/static/img/placeholder.svg'),
			('Mochila Executiva', 'Mochila resistente com compartimento para notebook', 189.90, 20, 'Acessórios', '/static/img/placeholder.svg'),
			('Óculos de Sol', 'Proteção UV400 estilosa', 129.90, 40, 'Acessórios', '/static/img/placeholder.svg'),
			('Calça Jeans', 'Calça jeans slim fit moderna', 159.90, 35, 'Roupas', '/static/img/placeholder.svg'),
			('Relógio Clássico', 'Relógio analógico com pulseira de couro', 299.90, 15, 'Acessórios', '/static/img/placeholder.svg')`,
	}
	for _, q := range queries {
		if _, err := DB.Exec(q); err != nil {
			log.Printf("migrate: %v", err)
		}
	}
}
