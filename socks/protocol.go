package socks

import (
	"bufio"
	"fmt"
	yaklog "github.com/yaklang/yaklang/common/log"
	"socks2https/pkg/comm"
)

func parseProtocol(reader *bufio.Reader) (int, error) {
	protocolHeader, err := reader.Peek(3)
	if err != nil {
		return TCP_PROTOCOL, fmt.Errorf("pre read Protocol Header failed : %v", err)
	}
	switch protocolHeader[0] {
	case Change_Cipher_Spec, Alert, Handshake, Application_Data, Heartbeat, TLS12CID, ACK, Encrypted_Handshake_Message:
		yaklog.Infof("%s %s", Tag, comm.SetColor(comm.YELLOW_BG_COLOR_TYPE, comm.SetColor(comm.RED_COLOR_TYPE, "Client use TSL connection")))
		return HTTPS_PROTOCOL, nil
	}
	switch string(protocolHeader) {
	case "CON":
		yaklog.Infof("%s %s", Tag, comm.SetColor(comm.RED_BG_COLOR_TYPE, comm.SetColor(comm.YELLOW_COLOR_TYPE, "Client use HTTP CONNECT connection")))
		return HTTPS_PROTOCOL, nil
	case "GET", "POS", "PUT", "DEL", "HEA", "OPT", "PAT", "TRA":
		yaklog.Infof("%s %s", Tag, comm.SetColor(comm.RED_COLOR_TYPE, "Client use HTTP connection"))
		return HTTP_PROTOCOL, nil
	}
	yaklog.Infof("%s Client use TCP connection", Tag)
	return TCP_PROTOCOL, nil
}