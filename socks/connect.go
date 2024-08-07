package socks

import (
	"fmt"
	yaklog "github.com/yaklang/yaklang/common/log"
	"github.com/yaklang/yaklang/common/netx"
	yakutils "github.com/yaklang/yaklang/common/utils"
	"io"
	"net"
	"socks2https/pkg/comm"
	"socks2https/setting"
)

// 客户端请求包
// +-----+-----+-------+------+----------+----------+
// | VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
// +-----+-----+-------+------+----------+----------+
// |  1  |  1  | X'00' |  1   | Variable |    2     |
// +-----+-----+-------+------+----------+----------+

// 服务端响应包
// +-----+-----+-------+------+----------+----------+
// | VER | REP |  RSV  | ATYP | BND.ADDR | BND.PORT |
// +-----+-----+-------+------+----------+----------+
// |  1  |  1  |   1   |   1  | Variable |    2     |
// +-----+-----+-------+------+----------+----------+

const (
	HTTP_PROTOCOL  int = 0
	HTTPS_PROTOCOL int = 1
	TCP_PROTOCOL   int = 2

	CONNECT_CMD       byte = 0x01
	BIND_CMD          byte = 0x02
	UDP_ASSOCIATE_CMD byte = 0x03

	RESERVED byte = 0x00

	IPV4_ATYPE byte = 0x01
	FQDN_ATYPE byte = 0x03
	IPV6_ATYPE byte = 0x04

	SUCCEEDED_REP                    byte = 0x00
	GENERAL_SOCKS_SERVER_FAILURE_REP byte = 0x01
	CONNECTION_NOT_ALLOWED_REP       byte = 0x02
	NETWORK_UNREACHABLE_REP          byte = 0x03
	HOST_UNREACHABLE_REP             byte = 0x04
	CONNECTION_REFUSED_REP           byte = 0x05
	TTL_EXPIRED_REP                  byte = 0x06
	COMMAND_NOT_SUPPORTED_REP        byte = 0x07
	ADDRESS_TYPE_NOT_SUPPORTED_REP   byte = 0x08
)

// 和远程服务器建立连接
func connect(src net.Conn) (net.Conn, int, byte, error) {
	protocol := TCP_PROTOCOL
	buf := make([]byte, 4)
	if _, err := io.ReadFull(src, buf); err != nil {
		yaklog.Errorf("read [%s] header buf failed", src.RemoteAddr().String())
		return nil, protocol, GENERAL_SOCKS_SERVER_FAILURE_REP, err
	}
	ver, cmd, rsv, aTyp := buf[0], buf[1], buf[2], buf[3]
	yaklog.Debugf("VER : %v , CMD : %v , RSA : %v , ATYP : %v", ver, cmd, rsv, aTyp)
	if ver != SOCKS5_VERSION {
		return nil, protocol, GENERAL_SOCKS_SERVER_FAILURE_REP, fmt.Errorf("not support socks version : %v", ver)
	} else if cmd != CONNECT_CMD {
		return nil, protocol, COMMAND_NOT_SUPPORTED_REP, fmt.Errorf("not support method : %v", cmd)
	} else if rsv != RESERVED {
		return nil, protocol, CONNECTION_NOT_ALLOWED_REP, fmt.Errorf("nuknow reserved : %v", rsv)
	}
	var host string
	switch aTyp {
	case IPV6_ATYPE:
		buf = make([]byte, net.IPv6len)
		fallthrough
	case IPV4_ATYPE:
		if _, err := io.ReadFull(src, buf); err != nil {
			return nil, protocol, GENERAL_SOCKS_SERVER_FAILURE_REP, fmt.Errorf("read [%s] ip address buf failed", src.RemoteAddr().String())
		}
		host = net.IP(buf).String()
	case FQDN_ATYPE:
		if _, err := io.ReadFull(src, buf[:1]); err != nil {
			return nil, protocol, GENERAL_SOCKS_SERVER_FAILURE_REP, fmt.Errorf("read [%s] fqdn address length buf failed", src.RemoteAddr().String())
		}
		aLen := buf[0]
		yaklog.Debugf("ALEN : %v", aLen)
		if aLen > net.IPv4len {
			buf = make([]byte, aLen)
		}
		if _, err := io.ReadFull(src, buf[:aLen]); err != nil {
			return nil, protocol, GENERAL_SOCKS_SERVER_FAILURE_REP, fmt.Errorf("read [%s] fqdn address buf failed", src.RemoteAddr().String())
		}
		host = string(buf[:aLen])
	default:
		return nil, TCP_PROTOCOL, ADDRESS_TYPE_NOT_SUPPORTED_REP, fmt.Errorf("not support address type : %v", aTyp)
	}
	if _, err := io.ReadFull(src, buf[:2]); err != nil {
		return nil, TCP_PROTOCOL, ADDRESS_TYPE_NOT_SUPPORTED_REP, fmt.Errorf("read [%s] port buf failed", src.RemoteAddr().String())
	}
	port := (uint16(buf[0]) << 8) + uint16(buf[1])
	addr := fmt.Sprintf("%s:%d", host, port)
	yaklog.Debugf("target address : %s", comm.SetColor(comm.RED_COLOR_TYPE, addr))
	dst, err := net.DialTimeout(PROTOCOL_TCP, addr, setting.TargetTimeout)
	if err != nil {
		return nil, protocol, CONNECTION_REFUSED_REP, fmt.Errorf("connect to target host failed : %v", err)
	}
	//根据端口号判断协议类型
	switch port {
	case 443:
		protocol = HTTPS_PROTOCOL
		dst, err = netx.DialTCPTimeout(setting.TargetTimeout, addr, setting.Proxy)
		if err != nil {
			return nil, protocol, CONNECTION_REFUSED_REP, fmt.Errorf("connect to target host failed : %v", err)
		}
	case 80:
		protocol = HTTP_PROTOCOL
	}
	return dst, protocol, SUCCEEDED_REP, nil
}

func failure(conn io.Writer, rep byte) error {
	_, err := conn.Write([]byte{SOCKS5_VERSION, rep, RESERVED, IPV4_ATYPE, 0, 0, 0, 0, 0, 0})
	return err
}

func success(conn net.Conn) error {
	yaklog.Debugf("bound : %v", comm.SetColor(comm.RED_COLOR_TYPE, fmt.Sprintf("%v", setting.Bound)))
	if !setting.Bound {
		yaklog.Info(comm.SetColor(comm.GREEN_COLOR_TYPE, "no need dns lookup"))
		_, err := conn.Write([]byte{SOCKS5_VERSION, SUCCEEDED_REP, RESERVED, IPV4_ATYPE, 0, 0, 0, 0, 0, 0})
		return err
	}
	yaklog.Info(comm.SetColor(comm.RED_COLOR_TYPE, "need dns lookup"))
	addr, port, err := yakutils.ParseStringToHostPort(conn.LocalAddr().String())
	if err != nil {
		return err
	}
	var host []byte
	var aTyp, aLen byte
	if yakutils.IsIPv4(addr) {
		aTyp = IPV4_ATYPE
		host = net.ParseIP(addr).To4()
	} else if yakutils.IsIPv6(addr) {
		aTyp = IPV6_ATYPE
		host = net.ParseIP(addr).To16()
	} else {
		aTyp = FQDN_ATYPE
		aLen = byte(len(addr))
	}
	buf := make([]byte, 2)
	buf[0] = byte(port >> 8)
	buf[1] = byte(uint16(port) - uint16(buf[0])<<8)
	if aLen != 0 {
		_, err = conn.Write(append(append([]byte{SOCKS5_VERSION, SUCCEEDED_REP, RESERVED, aTyp, aLen}, host...), buf...))
		return err
	}
	_, err = conn.Write(append(append([]byte{SOCKS5_VERSION, SUCCEEDED_REP, RESERVED, aTyp}, host...), buf...))
	return nil
}
