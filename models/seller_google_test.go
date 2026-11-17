package models

import (
	"errors"
	"testing"
)

func TestVendedorEntraComGoogleSoDepoisDeVincular(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")

	if _, err := SellerByGoogle("sub-ana"); !errors.Is(err, ErrSellerGoogleNotLinked) {
		t.Fatalf("login sem vínculo aceito: %v", err)
	}
	if err := LinkSellerGoogle(ana, "sub-ana"); err != nil {
		t.Fatal(err)
	}
	s, err := SellerByGoogle("sub-ana")
	if err != nil || s.ID != ana || !SellerGoogleLinked(ana) {
		t.Fatalf("vendedor = %+v, %v", s, err)
	}
	// A mesma conta Google não entra em dois vendedores.
	bia := vendedorConectado(t, "bia", cpfTeste2, "222")
	if err := LinkSellerGoogle(bia, "sub-ana"); !errors.Is(err, ErrGoogleAccountConflict) {
		t.Fatalf("conta Google em dois vendedores: %v", err)
	}
	// Vendedor suspenso não entra pelo Google.
	SetSellerActive(ana, false)
	if _, err := SellerByGoogle("sub-ana"); !errors.Is(err, ErrSellerGoogleNotLinked) {
		t.Fatalf("vendedor suspenso entrou: %v", err)
	}
	SetSellerActive(ana, true)
	UnlinkSellerGoogle(ana)
	if _, err := SellerByGoogle("sub-ana"); !errors.Is(err, ErrSellerGoogleNotLinked) {
		t.Fatal("entrou depois de desvincular")
	}
}
