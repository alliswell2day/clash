package tunnel

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	adapters "github.com/Dreamacro/clash/adapters/inbound"
	"github.com/Dreamacro/clash/common/pool"
)

func (t *Tunnel) handleHTTP(request *adapters.HTTPAdapter, outbound net.Conn) {
	conn := newTrafficTrack(outbound, t.traffic)
	req := request.R
	host := req.Host

	for {
		keepAlive := strings.TrimSpace(strings.ToLower(req.Header.Get("Proxy-Connection"))) == "keep-alive"

		req.Header.Set("Connection", "close")
		req.RequestURI = ""
		adapters.RemoveHopByHopHeaders(req.Header)
		err := req.Write(conn)
		if err != nil {
			break
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			break
		}
		adapters.RemoveHopByHopHeaders(resp.Header)
		if resp.ContentLength >= 0 {
			resp.Header.Set("Proxy-Connection", "keep-alive")
			resp.Header.Set("Connection", "keep-alive")
			resp.Header.Set("Keep-Alive", "timeout=4")
			resp.Close = false
		} else {
			resp.Close = true
		}
		err = resp.Write(request)
		if err != nil || resp.Close {
			break
		}

		if !keepAlive {
			break
		}

		req, err = http.ReadRequest(bufio.NewReader(request))
		if err != nil {
			break
		}

		// Sometimes firefox just open a socket to process multiple domains in HTTP
		// The temporary solution is close connection when encountering different HOST
		if req.Host != host {
			break
		}
	}
}

func (t *Tunnel) handleUDPToRemote(conn net.Conn, pc net.PacketConn, addr net.Addr) {
	buf := pool.BufPool.Get().([]byte)
	defer pool.BufPool.Put(buf[:cap(buf)])

	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	if _, err = pc.WriteTo(buf[:n], addr); err != nil {
		return
	}
	t.traffic.Up() <- int64(n)
}

func (t *Tunnel) handleUDPToLocal(conn net.Conn, pc net.PacketConn) {
	buf := pool.BufPool.Get().([]byte)
	defer pool.BufPool.Put(buf[:cap(buf)])

	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		n, err = conn.Write(buf[:n])
		if err != nil {
			return
		}
		t.traffic.Down() <- int64(n)
	}
}

func (t *Tunnel) handleSocket(request *adapters.SocketAdapter, outbound net.Conn) {
	conn := newTrafficTrack(outbound, t.traffic)
	relay(request, conn)
}

// relay copies between left and right bidirectionally.
func relay(leftConn, rightConn net.Conn) {
	ch := make(chan error)

	go func() {
		buf := pool.BufPool.Get().([]byte)
		_, err := io.CopyBuffer(leftConn, rightConn, buf)
		pool.BufPool.Put(buf[:cap(buf)])
		leftConn.SetReadDeadline(time.Now())
		ch <- err
	}()

	buf := pool.BufPool.Get().([]byte)
	io.CopyBuffer(rightConn, leftConn, buf)
	pool.BufPool.Put(buf[:cap(buf)])
	rightConn.SetReadDeadline(time.Now())
	<-ch
}
