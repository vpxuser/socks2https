package mitm

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	yaklog "github.com/yaklang/yaklang/common/log"
	"io"
	"net"
	"socks2https/pkg/cert"
	"socks2https/pkg/comm"
	"socks2https/pkg/crypt"
)

type HandleRecord func(reader *bufio.Reader, conn net.Conn, ctx *Context)

var TLSMITMPipeLine []HandleRecord

func readUnknownRecord(record []byte, ctx *Context) (*Record, error) {
	switch ctx.ClientEncrypted {
	case true:
		unkonwnRecord, err := ParseBlockRecord(record, ctx)
		if err != nil {
			return nil, err
		}
		return unkonwnRecord, nil
	default:
		unkonwnRecord, err := ParseRecord(record, ctx)
		if err != nil {
			return nil, err
		}
		return unkonwnRecord, nil
	}
}

func filterRecord(reader *bufio.Reader, contentType uint8, handshakeType uint8, ctx *Context) (*Record, error) {
	header := make([]byte, 5)
	if _, err := reader.Read(header); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read TLS Record Header failed : %v", err)
	}
	length := binary.BigEndian.Uint16(header[3:5])
	fragment := make([]byte, length)
	if _, err := reader.Read(fragment); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read TLS Record Fragment failed : %v", err)
	}
	record, err := readUnknownRecord(append(header, fragment...), ctx)
	if err != nil {
		return nil, err
	} else if record.ContentType != contentType {
		switch record.ContentType {
		case ContentTypeAlert:
			alertLevel := comm.SetColor(comm.RED_COLOR_TYPE, AlertLevel[record.Alert.Level])
			alertDescription := comm.SetColor(comm.RED_COLOR_TYPE, AlertDescription[record.Alert.Description])
			return nil, fmt.Errorf("[%s] [%s] %s", comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), alertLevel, alertDescription)
		default:
			return nil, fmt.Errorf("not supported Content Type : [%v] : %d", ContentType[record.ContentType], record.ContentType)
		}
	} else if record.ContentType == ContentTypeHandshake && record.Handshake.HandshakeType != handshakeType {
		return nil, fmt.Errorf("[%s] [%s] Unknown Handshake Type : %d", comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.YELLOW_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]), record.Handshake.HandshakeType)
	}
	return record, nil
}

func SendAlert(level, description uint8, conn net.Conn, ctx *Context) {
	record := NewAlert(level, description)
	alert := record.GetRaw()
	if ctx.ServerEncrypted {
		var err error
		alert, err = NewBlockRecord(record, ctx)
		if err != nil {
			yaklog.Warnf("%s %s", ctx.Mitm2ClientLog, comm.SetColor(comm.MAGENTA_COLOR_TYPE, fmt.Sprintf("create Block Alert failed : %v", err)))
			return
		}
	}
	if _, err := conn.Write(alert); err != nil {
		yaklog.Warnf("%s %s", ctx.Mitm2ClientLog, comm.SetColor(comm.MAGENTA_COLOR_TYPE, fmt.Sprintf("write Alert failed : %v", err)))
		return
	}
}

var ReadClientHello = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := filterRecord(reader, ContentTypeHandshake, HandshakeTypeClientHello, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, record.Fragment)
	clientHello := record.Handshake.ClientHello
	ctx.ClientRandom = record.Handshake.ClientHello.Random
	ok := false
	for _, cipherSuite := range clientHello.CipherSuites {
		if cipherSuite == ctx.CipherSuite {
			ok = true
			break
		}
	}
	if !ok {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, "not support CipherSuites")
		return
	}
	for _, extension := range clientHello.Extensions {
		if extension.Type == ExtensionTypeServerName {
			ctx.Domain = extension.ServerName.List[0].Name
			break
		}
	}
	if ctx.Domain == "" {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, "Domain is not exist")
		return
	}
	contentType := comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType])
	handshakeType := comm.SetColor(comm.RED_BG_COLOR_TYPE, comm.SetColor(comm.YELLOW_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	yaklog.Debugf("%s [%s] [%s] Domain : %s", ctx.Client2MitmLog, contentType, handshakeType, ctx.Domain)
})

var WriteServerHello = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := NewServerHello(ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	serverHello := record.GetRaw()
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, serverHello[5:])
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	if _, err = conn.Write(serverHello); err != nil {
		yaklog.Errorf("%s write Server Hello failed : %v", ctx.Mitm2ClientLog, err)
		return
	}
})

var WriteCertificate = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := NewCertificate(cert.CertificateAndPrivateKeyPath, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, record.Fragment)
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	if _, err = conn.Write(record.GetRaw()); err != nil {
		yaklog.Errorf("%s write Certificate failed : %v", ctx.Mitm2ClientLog, err)
		return
	}
})

var WriteServerHelloDone = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record := NewServerHelloDone(ctx)
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, record.Fragment)
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	if _, err := conn.Write(record.GetRaw()); err != nil {
		yaklog.Errorf("%s write Server Hello Done failed : %v", ctx.Mitm2ClientLog, err)
		return
	}
})

var ReadClientKeyExchange = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := filterRecord(reader, ContentTypeHandshake, HandshakeTypeClientKeyExchange, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Mitm2ClientLog, err)
		return
	}
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, record.Fragment)
	yaklog.Debugf("%s [%s] [%s]", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	clientKeyExchange := record.Handshake.ClientKeyExchange.(*RSAClientKeyExchange)
	preMasterSecret, err := crypt.DecryptRSAPKCS(ctx.KeyDER, clientKeyExchange.EncrypedPreMasterSecret)
	if err != nil {
		yaklog.Errorf("%s RSA Decrypt PreMasterSecret failed : %v", ctx.Mitm2ClientLog, err)
		return
	}
	ctx.PreMasterSecret = preMasterSecret
	version := binary.BigEndian.Uint16(preMasterSecret[:2])
	masterSecret := PRF[version](preMasterSecret, []byte(LabelMasterSecret), append(ctx.ClientRandom[:], ctx.ServerRandom[:]...), len(preMasterSecret))
	ctx.MasterSecret = masterSecret
	ctx.KeyBlock = PRF[version](masterSecret, []byte(LabelKeyExpansion), append(ctx.ServerRandom[:], ctx.ClientRandom[:]...), 2*(ctx.MACLength+2*ctx.BlockLength))
	ctx.ClientMACKey, ctx.ServerMACKey = ctx.KeyBlock[:ctx.MACLength], ctx.KeyBlock[ctx.MACLength:2*ctx.MACLength]
	ctx.ClientKey, ctx.ServerKey = ctx.KeyBlock[2*ctx.MACLength:2*ctx.MACLength+ctx.BlockLength], ctx.KeyBlock[2*ctx.MACLength+ctx.BlockLength:2*(ctx.MACLength+ctx.BlockLength)]
	ctx.ClientIV, ctx.ServerIV = ctx.KeyBlock[2*(ctx.MACLength+ctx.BlockLength):2*(ctx.MACLength+ctx.BlockLength)+ctx.BlockLength], ctx.KeyBlock[2*(ctx.MACLength+ctx.BlockLength)+ctx.BlockLength:]
})

var ReadChangeCipherSpec = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := filterRecord(reader, ContentTypeChangeCipherSpec, 0xff, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Mitm2ClientLog, err)
		return
	}
	ctx.ClientEncrypted = true
	yaklog.Debugf("%s [%s] Raw : %v", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), record.Fragment)
})

var ReadFinished = HandleRecord(func(reader *bufio.Reader, conn net.Conn, ctx *Context) {
	record, err := filterRecord(reader, ContentTypeHandshake, HandshakeTypeFinished, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	yaklog.Debugf("%s [%s] [%s]", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[record.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[record.Handshake.HandshakeType]))
	verifyData := VerifyPRF(ctx.Version, ctx.MasterSecret, []byte(LabelClientFinished), ctx.HandshakeMessages, 12)
	if hmac.Equal(verifyData, record.Handshake.Payload) {
		yaklog.Infof("%s Verify Finished Successfully", ctx.Client2MitmLog)
	} else {
		yaklog.Infof("%s Verify Finished Failed", ctx.Client2MitmLog)
	}
})

func TLSMITM(reader *bufio.Reader, client net.Conn, ctx *Context) {
	defer client.Close()

	ctx.HandshakeType = HandshakeTypeClientHello
	clientHelloRaw, err := FilterRecord(reader, ContentTypeHandshake, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, clientHelloRaw[5:])
	clientHello, err := ParseRecord(clientHelloRaw, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.ClientHello = *clientHello
	domain, ok := clientHello.GetDomain()
	if !ok {
		yaklog.Errorf("%s Domain is empty", ctx.Client2MitmLog)
		return
	}
	ctx.Domain = domain
	yaklog.Debugf("%s [%s] [%s] Domain : %s", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[clientHello.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[clientHello.Handshake.HandshakeType]), domain)

	serverHello, err := NewServerHello(clientHello, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.ServerHello = *serverHello
	serverHelloRaw := serverHello.GetRaw()
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, serverHelloRaw[5:])
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[serverHello.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[serverHello.Handshake.HandshakeType]))
	if _, err = client.Write(serverHelloRaw); err != nil {
		yaklog.Errorf("%s write Server Hello failed : %v", ctx.Mitm2ClientLog, err)
		return
	}

	certificate, err := NewCertificate(cert.CertificateAndPrivateKeyPath, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.Certificate = *certificate
	certificateRaw := certificate.GetRaw()
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, certificateRaw[5:])
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[certificate.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[certificate.Handshake.HandshakeType]))
	if _, err = client.Write(certificateRaw); err != nil {
		yaklog.Errorf("%s write Certificate failed : %v", ctx.Mitm2ClientLog, err)
		return
	}

	serverHelloDone := NewServerHelloDone(ctx)
	ctx.ServerHelloDone = *serverHelloDone
	serverHelloDoneRaw := serverHelloDone.GetRaw()
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, serverHelloDoneRaw[5:])
	yaklog.Debugf("%s [%s] [%s]", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[serverHelloDone.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[serverHelloDone.Handshake.HandshakeType]))
	if _, err = client.Write(serverHelloDoneRaw); err != nil {
		yaklog.Errorf("%s write Server Hello Done failed : %v", ctx.Mitm2ClientLog, err)
		return
	}

	ctx.HandshakeType = HandshakeTypeClientKeyExchange
	clientKeyExchangeRaw, err := FilterRecord(reader, ContentTypeHandshake, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.HandshakeMessages = append(ctx.HandshakeMessages, clientKeyExchangeRaw[5:])
	clientKeyExchange, err := ParseRecord(clientKeyExchangeRaw, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	ctx.ClientKeyExchange = *clientKeyExchange
	//clientKeyExchangeJSON, _ := json.MarshalIndent(clientKeyExchange.Handshake.ClientKeyExchange.(*RSAClientKeyExchange), "", "  ")
	//yaklog.Debugf("%s Content Type : %s , Handshake Type : %s , Client Key Exchange :\n%s", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[clientKeyExchange.ContentType]), comm.SetColor(comm.RED_BG_COLOR_TYPE, comm.SetColor(comm.YELLOW_COLOR_TYPE, HandshakeType[clientKeyExchange.Handshake.HandshakeType])), clientKeyExchangeJSON)
	yaklog.Debugf("%s [%s] [%s]", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[clientKeyExchange.ContentType]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[clientKeyExchange.Handshake.HandshakeType]))

	ctx.HandshakeType = 0xFF
	changeCipherSpecRaw, err := FilterRecord(reader, ContentTypeChangeCipherSpec, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	//todo NewChangeCipherSpec()
	yaklog.Debugf("%s [%s] Raw : %v", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[changeCipherSpecRaw[0]]), changeCipherSpecRaw)

	ctx.HandshakeType = 0xFF
	clientFinishedRaw, err := FilterRecord(reader, ContentTypeHandshake, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	yaklog.Debugf("%s [%s] [%s]", ctx.Client2MitmLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[clientFinishedRaw[0]]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[HandshakeTypeFinished]))
	clientFinished, err := ParseRecord(clientFinishedRaw, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	//ctx.HandshakeMessages = append(ctx.HandshakeMessages, clientFinishedRaw)
	ctx.Finished = *clientFinished

	serverChangeCipherSpec := NewChangeCipherSpec()
	serverChangeCipherSpecRaw := serverChangeCipherSpec.GetRaw()
	yaklog.Debugf("%s [%s] Raw : %v", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[serverChangeCipherSpec.ContentType]), serverChangeCipherSpecRaw)
	if _, err = client.Write(serverChangeCipherSpecRaw); err != nil {
		yaklog.Errorf("%s write Change Cipher Spec failed : %v", ctx.Mitm2ClientLog, err)
		return
	}

	serverFinished := NewFinished(crypt.LabelServerFinished, ctx).GetRaw()
	yaklog.Debugf("%s Finished Length : %d , Finished : %v", ctx.Mitm2ClientLog, len(serverFinished), serverFinished)
	keyStore := clientKeyExchange.Handshake.ClientKeyExchange.(*RSAClientKeyExchange)
	macFinished := append(serverFinished[5:], crypt.MAC(keyStore.ServerMacKey, ctx.ServerSeqNum, serverFinished, sha1.New)...)
	yaklog.Debugf("%s MAC Finished Length : %d , MAC Finished : %v", ctx.Mitm2ClientLog, len(macFinished), macFinished)
	paddingFinished := crypt.Pad(macFinished, len(keyStore.ServerKey))
	yaklog.Debugf("%s Pidding Finished Raw Length : %d , Pidding Finished Raw : %v", ctx.Mitm2ClientLog, len(paddingFinished), paddingFinished)
	encryptedFinished, err := crypt.AESCBCEncrypt(paddingFinished, keyStore.ServerKey, keyStore.ServerIV)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
	yaklog.Debugf("%s Encrypted Finished Raw Length : %d , Encrypted Finished Raw : %v", ctx.Mitm2ClientLog, len(encryptedFinished), encryptedFinished)
	//finalFragment := append(keyStore.ServerIV, encryptedFinished...)
	finalFragment := encryptedFinished
	yaklog.Debugf("%s Final Fragment Raw Length : %d , Final Fragment Raw : %v", ctx.Mitm2ClientLog, len(finalFragment), finalFragment)
	finalLength := make([]byte, 2)
	binary.BigEndian.PutUint16(finalLength, uint16(len(finalFragment)))
	finalFinished := append(append(serverFinished[:3], finalLength...), finalFragment...)
	yaklog.Debugf("%s Final Finished Raw Length : %d , Final Finished Raw : %v", ctx.Mitm2ClientLog, len(finalFinished), finalFinished)
	if _, err = client.Write(finalFinished); err != nil {
		yaklog.Errorf("%s write Finished failed : %v", ctx.Mitm2ClientLog, err)
	}
	yaklog.Debugf("%s [%s] [%s] Raw : %v", ctx.Mitm2ClientLog, comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[serverFinished[0]]), comm.SetColor(comm.RED_COLOR_TYPE, HandshakeType[HandshakeTypeFinished]), finalFinished)

	ctx.HandshakeType = 0xFF
	_, err = FilterRecord(reader, ContentTypeApplicationData, ctx)
	if err != nil {
		yaklog.Errorf("%s %v", ctx.Client2MitmLog, err)
		return
	}
}

func FilterRecord(reader *bufio.Reader, contentType uint8, ctx *Context) ([]byte, error) {
	recordHeader := make([]byte, 5)
	if _, err := reader.Read(recordHeader); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read TLS Record Header from Client failed : %v", err)
	}
	if recordHeader[0] != contentType {
		return nil, ReadUnkonwnRecord(recordHeader, reader, ctx)
	}
	length := binary.BigEndian.Uint16(recordHeader[3:5])
	recordFragment := make([]byte, length)
	if _, err := reader.Read(recordFragment); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read TLS Record Fragment failed : %v", err)
	}
	record := append(recordHeader, recordFragment...)
	if recordFragment[0] == ctx.HandshakeType || ctx.HandshakeType == 0xFF {
		return record, nil
	}
	return nil, ReadUnknownHandshake(record, reader, ctx)
}

func ReadUnkonwnRecord(recordHeader []byte, reader *bufio.Reader, ctx *Context) error {
	switch contentType := recordHeader[0]; contentType {
	case ContentTypeAlert:
		length := binary.BigEndian.Uint16(recordHeader[3:5])
		recordFragment := make([]byte, length)
		if n, err := reader.Read(recordFragment); err != nil && err != io.EOF || n != int(length) {
			return fmt.Errorf("read TLS Record Fragment failed : %v", err)
		}
		alert, err := ParseRecord(append(recordHeader, recordFragment...), ctx)
		if err != nil {
			return err
		}
		alertLevel := comm.SetColor(comm.RED_COLOR_TYPE, AlertLevel[alert.Alert.Level])
		alertDescription := comm.SetColor(comm.RED_COLOR_TYPE, AlertDescription[alert.Alert.Description])
		return fmt.Errorf("[%s] [%s] %s", comm.SetColor(comm.YELLOW_COLOR_TYPE, ContentType[contentType]), alertLevel, alertDescription)
	default:
		return fmt.Errorf("not supported Content Type : [%v] : %d", ContentType[contentType], contentType)
	}
}

func ReadUnknownHandshake(record []byte, reader *bufio.Reader, ctx *Context) error {
	//todo
	return fmt.Errorf("unknown Handshake Type")
}