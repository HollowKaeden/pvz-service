package httptransport

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// clientIP возвращает IP клиента. Заголовку X-Forwarded-For можно верить, только
// если запрос пришёл от доверенного прокси: иначе клиент подставит туда любой
// адрес сам.
//
// Если соединение от доверенного прокси, заголовок читается справа налево:
// адреса доверенных прокси пропускаются, первый недоверенный и есть клиент.
// Левее него стоит то, что прислал сам клиент, и этому не верим.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	ip := addrPort.Addr().Unmap()
	if !isTrusted(ip, trusted) {
		return ip.String()
	}

	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for _, hop := range slices.Backward(hops) {
		addr, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil {
			// Испорченный заголовок: дальше по цепочке не идём, клиентом
			// считаем последний разобранный адрес.
			break
		}
		ip = addr.Unmap()
		if !isTrusted(ip, trusted) {
			break
		}
	}
	return ip.String()
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
