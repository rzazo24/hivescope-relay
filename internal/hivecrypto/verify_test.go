package hivecrypto

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// signMessage firma message exactamente como lo hace Hive Keychain /
// hive-tx / dhive: sha256(message) + firma compacta recuperable secp256k1
// sobre una clave comprimida.
func signMessage(t *testing.T, priv *btcec.PrivateKey, message []byte) string {
	t.Helper()
	hash := sha256.Sum256(message)
	sig := ecdsa.SignCompact(priv, hash[:], true)
	return hex.EncodeToString(sig)
}

func TestVerifySignature_ValidSignatureMatchesExpectedKey(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("no se pudo generar clave privada de prueba: %v", err)
	}

	hivePubKey := EncodePublicKey(priv.PubKey().SerializeCompressed())
	if hivePubKey[:3] != "STM" {
		t.Fatalf("se esperaba prefijo STM, se obtuvo %q", hivePubKey)
	}

	message := []byte("hivescope-relay-link:deadbeef")
	sigHex := signMessage(t, priv, message)

	ok, err := VerifySignature(message, sigHex, hivePubKey)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !ok {
		t.Fatal("se esperaba que la firma fuera válida contra su propia clave pública")
	}
}

func TestVerifySignature_RejectsWrongKey(t *testing.T) {
	signerKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("no se pudo generar clave privada de prueba: %v", err)
	}
	otherKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("no se pudo generar clave privada de prueba: %v", err)
	}

	unrelatedHivePubKey := EncodePublicKey(otherKey.PubKey().SerializeCompressed())

	message := []byte("hivescope-relay-link:deadbeef")
	sigHex := signMessage(t, signerKey, message)

	ok, err := VerifySignature(message, sigHex, unrelatedHivePubKey)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if ok {
		t.Fatal("la firma no debería ser válida contra una clave que no la produjo")
	}
}

func TestVerifySignature_RejectsMalformedHex(t *testing.T) {
	_, err := VerifySignature([]byte("msg"), "not-hex", "STMxxx")
	if err == nil {
		t.Fatal("se esperaba un error por hex inválido")
	}
}

func TestVerifySignature_RejectsWrongLength(t *testing.T) {
	_, err := VerifySignature([]byte("msg"), "aabbcc", "STMxxx")
	if err == nil {
		t.Fatal("se esperaba un error por longitud de firma incorrecta")
	}
}
