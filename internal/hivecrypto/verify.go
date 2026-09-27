// Package hivecrypto verifica firmas producidas con claves de Hive
// (secp256k1, formato "compacto y recuperable" — el mismo que usan
// Hive Keychain, hive-tx y dhive) y codifica/decodifica claves públicas
// en el formato de Hive/Steem ("STM...").
package hivecrypto

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil/base58"
	"golang.org/x/crypto/ripemd160"
)

// hivePublicKeyPrefix es el prefijo que Hive/Steem anteponen a toda clave
// pública codificada en base58 (equivalente al "bitcoin address version
// byte", pero como texto en vez de un byte de versión numérico).
const hivePublicKeyPrefix = "STM"

// EncodePublicKey codifica una clave pública secp256k1 comprimida (33 bytes)
// en el formato de texto que usa Hive: "STM" + base58(clave || checksum),
// donde checksum son los primeros 4 bytes de ripemd160(clave).
func EncodePublicKey(compressedPubKey []byte) string {
	h := ripemd160.New()
	h.Write(compressedPubKey)
	checksum := h.Sum(nil)[:4]

	payload := make([]byte, 0, len(compressedPubKey)+len(checksum))
	payload = append(payload, compressedPubKey...)
	payload = append(payload, checksum...)

	return hivePublicKeyPrefix + base58.Encode(payload)
}

// VerifySignature comprueba que sigHex sea una firma válida, en el formato
// "compacto y recuperable" de secp256k1 sobre sha256(message), producida por
// la clave privada correspondiente a expectedHivePubKey (ej. "STM6LLeg...").
//
// Este es exactamente el esquema que usa Hive Keychain cuando la dApp llama a
// `requestSignBuffer(account, message, keyType, callback)`: Keychain calcula
// sha256(message) y firma ese hash con la clave solicitada (posting/active/
// owner), devolviendo una firma hexadecimal de 65 bytes (130 caracteres hex).
//
// El primer valor de retorno solo es significativo cuando err == nil:
//   - (true, nil)  -> la firma es válida y corresponde a expectedHivePubKey.
//   - (false, nil) -> la firma tiene un formato correcto pero no corresponde
//     a esa clave (o fue producida por otra clave).
//   - (false, err) -> la firma está mal formada (no es hex, longitud incorrecta, etc).
func VerifySignature(message []byte, sigHex string, expectedHivePubKey string) (bool, error) {
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, errors.New("hive_sig no es hexadecimal válido")
	}
	if len(sigBytes) != 65 {
		return false, errors.New("hive_sig debe tener 65 bytes (130 caracteres hex)")
	}
	if expectedHivePubKey == "" {
		return false, errors.New("no se proporcionó una clave pública hive contra la cual verificar")
	}

	hash := sha256.Sum256(message)

	pubKey, _, err := ecdsa.RecoverCompact(sigBytes, hash[:])
	if err != nil {
		// La firma no es recuperable (bytes corruptos, recovery id inválido, etc):
		// esto no es un error del sistema, simplemente la firma es inválida.
		return false, nil
	}

	recovered := EncodePublicKey(pubKey.SerializeCompressed())
	return recovered == expectedHivePubKey, nil
}
