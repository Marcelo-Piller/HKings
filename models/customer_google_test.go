package models

import (
	"errors"
	"testing"
)

func TestGoogleCriaContaNovaSemSenha(t *testing.T) {
	setupDB(t)
	res, err := LoginWithGoogle("sub-1", "Nova@Gmail.com", "Nova Leitora", true)
	if err != nil || !res.Created {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if res.Customer.Email != "nova@gmail.com" || res.Customer.GoogleSub != "sub-1" || res.Customer.PasswordHash != "" {
		t.Fatalf("cliente = %+v", res.Customer)
	}
	// Conta sem senha não entra pelo formulário de senha, nem com senha vazia.
	if _, err := AuthenticateCustomer("nova@gmail.com", ""); !errors.Is(err, ErrUseGoogleLogin) {
		t.Fatalf("login por senha em conta do Google: err = %v", err)
	}

	again, err := LoginWithGoogle("sub-1", "nova@gmail.com", "", true)
	if err != nil || again.Created || again.Customer.ID != res.Customer.ID {
		t.Fatalf("segundo login criou outra conta: %+v, %v", again, err)
	}
}

func TestGoogleVinculaContaComSenhaEDesativaSenha(t *testing.T) {
	setupDB(t)
	id := criaCliente(t, "Leitor@Exemplo.com")
	res, err := LoginWithGoogle("sub-2", "leitor@exemplo.com", "Leitor", true)
	if err != nil || !res.Linked || res.Customer.ID != id {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	// Quem criou a conta com senha antes (talvez com o e-mail de outra pessoa)
	// não entra mais por ela.
	if _, err := AuthenticateCustomer("Leitor@Exemplo.com", "senha-do-leitor"); err == nil {
		t.Fatal("senha antiga continua funcionando depois do vínculo com Google")
	}
}

func TestGoogleExigeEmailVerificado(t *testing.T) {
	setupDB(t)
	criaCliente(t, "alvo@exemplo.com")
	if _, err := LoginWithGoogle("sub-3", "alvo@exemplo.com", "", false); !errors.Is(err, ErrGoogleEmailUnverified) {
		t.Fatalf("e-mail não verificado aceito: %v", err)
	}
	c, _ := GetCustomerByEmail("alvo@exemplo.com")
	if c.GoogleSub != "" {
		t.Fatal("conta vinculada com e-mail não verificado")
	}
}

func TestGoogleNaoTomaContaJaVinculada(t *testing.T) {
	setupDB(t)
	if _, err := LoginWithGoogle("sub-original", "dono@exemplo.com", "Dono", true); err != nil {
		t.Fatal(err)
	}
	if _, err := LoginWithGoogle("sub-intruso", "dono@exemplo.com", "Intruso", true); !errors.Is(err, ErrGoogleAccountConflict) {
		t.Fatalf("outra conta Google assumiu o cadastro: %v", err)
	}
}
