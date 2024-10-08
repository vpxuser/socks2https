package tlsutils

import (
	"crypto/sha256"
	"crypto/tls"
	"hash"
	"socks2https/context"
	"socks2https/pkg/crypt"
)

const (
	// TLS 1.2 Cipher Suites
	TLS_RSA_WITH_AES_128_CBC_SHA          uint16 = 0x002F
	TLS_RSA_WITH_AES_256_CBC_SHA          uint16 = 0x0035
	TLS_RSA_WITH_AES_128_CBC_SHA256       uint16 = 0x003C
	TLS_RSA_WITH_AES_256_CBC_SHA256       uint16 = 0x003D
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA    uint16 = 0xC013
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA    uint16 = 0xC014
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256 uint16 = 0xC027
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384 uint16 = 0xC028

	// TLS 1.3 Cipher Suites
	TLS_AES_128_GCM_SHA256       uint16 = 0x1301
	TLS_AES_256_GCM_SHA384       uint16 = 0x1302
	TLS_CHACHA20_POLY1305_SHA256 uint16 = 0x1303
	TLS_AES_128_CCM_SHA256       uint16 = 0x1304
	TLS_AES_128_CCM_8_SHA256     uint16 = 0x1305

	// Other Common Cipher Suites
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256       uint16 = 0xC02B
	TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384       uint16 = 0xC02C
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256         uint16 = 0xC02F
	TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384         uint16 = 0xC030
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 uint16 = 0xCCA9
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256   uint16 = 0xCCA8
)

// VerifyPRF 消息记录校验算法
func VerifyPRF(version uint16, secret, label []byte, handshakeMessages [][]byte, outputLength int) []byte {
	var hashFunc hash.Hash
	switch version {
	case tls.VersionTLS12:
		hashFunc = sha256.New()
	default:
		return nil
	}
	for _, message := range handshakeMessages {
		hashFunc.Write(message)
	}
	return crypt.PRF[version](secret, label, hashFunc.Sum(nil), outputLength)
}

func NewFinished(ctx *context.Context) *Record {
	verifyData := VerifyPRF(ctx.TLSContext.Version, ctx.TLSContext.MasterSecret, []byte(crypt.LabelServerFinished), ctx.TLSContext.HandshakeMessages, 12)

	handshake := &Handshake{
		HandshakeType: HandshakeTypeFinished,
		Length:        uint32(len(verifyData)),
		Payload:       verifyData,
		Finished:      verifyData,
	}
	handshakeRaw := handshake.GetRaw()
	return &Record{
		ContentType: ContentTypeHandshake,
		Version:     ctx.TLSContext.Version,
		Length:      uint16(len(handshakeRaw)),
		Fragment:    handshakeRaw,
		Handshake:   handshake,
	}
}
