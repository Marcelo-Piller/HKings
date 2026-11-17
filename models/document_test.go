package models

import "testing"

func TestValidateDocumentAceitaCPFValido(t *testing.T) {
	// CPFs de teste conhecidos, com dígito verificador correto.
	casos := []string{"529.982.247-25", "52998224725"}
	for _, c := range casos {
		if _, err := ValidateDocument(c); err != nil {
			t.Fatalf("CPF válido %q recusado: %v", c, err)
		}
	}
}

func TestValidateDocumentAceitaCNPJValido(t *testing.T) {
	casos := []string{"11.222.333/0001-81", "11222333000181"}
	for _, c := range casos {
		if _, err := ValidateDocument(c); err != nil {
			t.Fatalf("CNPJ válido %q recusado: %v", c, err)
		}
	}
}

func TestValidateDocumentRecusaDigitoVerificadorErrado(t *testing.T) {
	// Mesmo CPF válido acima, com o último dígito trocado.
	if _, err := ValidateDocument("529.982.247-26"); err == nil {
		t.Fatal("CPF com dígito verificador errado foi aceito")
	}
	if _, err := ValidateDocument("11.222.333/0001-82"); err == nil {
		t.Fatal("CNPJ com dígito verificador errado foi aceito")
	}
}

func TestValidateDocumentRecusaTodosDigitosIguais(t *testing.T) {
	// "111.111.111-11" passa no cálculo ingênuo do dígito verificador (o
	// checksum dá certo por coincidência), mas nunca foi emitido pela Receita
	// — é o golpe mais comum contra esse tipo de validação.
	casos := []string{"111.111.111-11", "00000000000", "11111111111111", "22222222222222"}
	for _, c := range casos {
		if _, err := ValidateDocument(c); err == nil {
			t.Fatalf("documento com dígitos repetidos foi aceito: %q", c)
		}
	}
}

func TestValidateDocumentRecusaTamanhoInvalido(t *testing.T) {
	casos := []string{"", "123", "123456789012", "123456789012345"}
	for _, c := range casos {
		if _, err := ValidateDocument(c); err == nil {
			t.Fatalf("documento de tamanho inválido foi aceito: %q", c)
		}
	}
}

func TestValidateDocumentNormalizaAntesDeValidar(t *testing.T) {
	normalized, err := ValidateDocument("529.982.247-25")
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "52998224725" {
		t.Fatalf("documento não normalizado: %q", normalized)
	}
}

func TestFormatDocumentAplicaMascara(t *testing.T) {
	if got := FormatDocument("52998224725"); got != "529.982.247-25" {
		t.Fatalf("máscara de CPF = %q", got)
	}
	if got := FormatDocument("11222333000181"); got != "11.222.333/0001-81" {
		t.Fatalf("máscara de CNPJ = %q", got)
	}
}
