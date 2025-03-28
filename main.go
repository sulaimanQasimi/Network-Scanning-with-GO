package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
	"strings"
	"strconv"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type ScanResult struct {
	IP   string
	Port int
	Open bool
}

func pingHost(ip string, timeout time.Duration) bool {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		fmt.Printf("Error creating ICMP listener: %v\n", err)
		return false
	}
	defer c.Close()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte(""),
		},
	}

	msgBytes, err := msg.Marshal(nil)
	if err != nil {
		return false
	}

	dest := net.ParseIP(ip)
	if _, err := c.WriteTo(msgBytes, &net.IPAddr{IP: dest}); err != nil {
		return false
	}

	c.SetReadDeadline(time.Now().Add(timeout))
	reply := make([]byte, 1500)
	_, _, err = c.ReadFrom(reply)
	return err == nil
}

func scanPort(ip string, port int, timeout time.Duration) ScanResult {
	target := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", target, timeout)

	result := ScanResult{IP: ip, Port: port}
	if err != nil {
		result.Open = false
		return result
	}
	conn.Close()
	result.Open = true
	return result
}

func generateIPs(startIP, endIP string) ([]string, error) {
	start := net.ParseIP(startIP).To4()
	end := net.ParseIP(endIP).To4()
	if start == nil || end == nil {
		return nil, fmt.Errorf("invalid IP address")
	}

	var ips []string
	for ip := start; ip != nil && bytes2int(ip) <= bytes2int(end); inc(ip) {
		ips = append(ips, ip.String())
	}
	return ips, nil
}

func bytes2int(b net.IP) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func getGatewayIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				// Assuming gateway is first host in network
				ip := ipnet.IP.To4()
				ip[3] = 1
				return ip.String()
			}
		}
	}
	return ""
}

func checkInternetConnectivity() bool {
	return pingHost("8.8.8.8", 2*time.Second)
}

func main() {
	mode := flag.String("mode", "range", "Scan mode: range, specific, gateway, internet")
	startIP := flag.String("start", "192.168.1.1", "Start IP address for range scan")
	endIP := flag.String("end", "192.168.1.255", "End IP address for range scan")
	specificIP := flag.String("ip", "", "Specific IP address to scan")
	portRange := flag.String("ports", "1-1024", "Port range to scan (e.g., 80 or 1-1024)")
	timeout := flag.Duration("timeout", 500*time.Millisecond, "Timeout for each scan")
	flag.Parse()

	switch *mode {
	case "internet":
		if checkInternetConnectivity() {
			fmt.Println("Internet is accessible (Google DNS 8.8.8.8 responds to ping)")
		} else {
			fmt.Println("No internet connectivity detected")
		}
		return

	case "gateway":
		gatewayIP := getGatewayIP()
		if gatewayIP == "" {
			fmt.Println("Could not determine gateway IP")
			return
		}
		*startIP = gatewayIP
		*endIP = gatewayIP

	case "specific":
		if *specificIP == "" {
			fmt.Println("Please provide a specific IP address using -ip flag")
			return
		}
		*startIP = *specificIP
		*endIP = *specificIP
	}

	var ips []string
	var err error
	ips, err = generateIPs(*startIP, *endIP)
	if err != nil {
		fmt.Printf("Error generating IP range: %v\n", err)
		return
	}

	ports := make([]int, 0)
	portParts := strings.Split(*portRange, "-")
	startPort, _ := strconv.Atoi(portParts[0])
	endPort := startPort
	if len(portParts) > 1 {
		endPort, _ = strconv.Atoi(portParts[1])
	}

	for i := startPort; i <= endPort; i++ {
		ports = append(ports, i)
	}

	var wg sync.WaitGroup
	results := make(chan ScanResult, len(ips)*len(ports))
	activeHosts := make(map[string]bool)
	var hostMutex sync.Mutex

	for _, ip := range ips {
		if pingHost(ip, *timeout) {
			fmt.Printf("Host %s is up, scanning ports...\n", ip)
			hostMutex.Lock()
			activeHosts[ip] = true
			hostMutex.Unlock()
			for _, port := range ports {
				wg.Add(1)
				go func(ip string, port int) {
					defer wg.Done()
					results <- scanPort(ip, port, *timeout)
				}(ip, port)
			}
		} else {
			fmt.Printf("Host %s is down, skipping...\n", ip)
		}
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	openPorts := make(map[string][]int)
	for result := range results {
		if result.Open {
			openPorts[result.IP] = append(openPorts[result.IP], result.Port)
		}
	}

	fmt.Printf("\nScan Summary:\n")
	fmt.Printf("Total active hosts found: %d\n", len(activeHosts))
	for ip := range activeHosts {
		if ports, ok := openPorts[ip]; ok {
			fmt.Printf("Host %s has %d open ports: %v\n", ip, len(ports), ports)
		} else {
			fmt.Printf("Host %s is up but has no open ports in the specified range\n", ip)
		}
	}
}