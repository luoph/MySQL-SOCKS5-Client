package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

type ConfigStruct struct {
	Name        string `json:"name"`
	Socks5Ip    string `json:"socks5_ip"`
	Socks5Port  string `json:"socks5_port"`
	ServiceIp   string `json:"service_ip"`
	ServicePort string `json:"service_port"`
	ListenAddr  string `json:"listen_addr"`
	ListenPort  string `json:"listen_port"`
}

func main() {
	configPath := flag.String("config", "/etc/remote-service-proxy.json", "config file path")
	flag.Parse()

	fmt.Println("configPath:", *configPath)

	// 打开json文件
	jsonFile, err := os.Open(*configPath)
	if err != nil {
		fmt.Println(err)
	}

	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)

	var configStructs []ConfigStruct
	json.Unmarshal([]byte(byteValue), &configStructs)

	wg := sync.WaitGroup{}
	wg.Add(2)
	for _, configStruct := range configStructs {
		go createServer(configStruct, &wg)
	}
	wg.Wait()
}

func createServer(configStruct ConfigStruct, wg *sync.WaitGroup) {
	defer wg.Done()
	var l net.Listener
	var err error

	l, err = net.Listen("tcp", configStruct.ListenAddr+":"+configStruct.ListenPort)
	if err != nil {
		fmt.Println("Error listening:", err)
		os.Exit(1)
	}

	defer l.Close()
	fmt.Println("Name: " + configStruct.Name + " Listening on " + configStruct.ListenAddr + ":" + configStruct.ListenPort)
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting: ", err)
			os.Exit(1)
		}
		//logs an incoming message
		fmt.Printf("create connection %s -> %s \n", conn.RemoteAddr(), conn.LocalAddr())
		// Handle connections in a new goroutine.
		go handleServerConn(conn, configStruct)
	}
}

func handleServerConn(localConn net.Conn, configStruct ConfigStruct) {
	defer localConn.Close()
	var stage = 0

	// 连接远端
	remoteConn, err := net.Dial("tcp", configStruct.Socks5Ip+":"+configStruct.Socks5Port)
	if err != nil {
		fmt.Println("remote connection error:", err.Error())
		return
	}
	defer remoteConn.Close()
	stage = 1

	// 发送socks握手信息
	_, err = remoteConn.Write([]byte{05, 01, 00})
	if err != nil {
		fmt.Println("send socks handshake error:", err.Error())
		return
	}
	stage = 2

	// 接收socks握手信息
	buf := make([]byte, 2)
	len, err := remoteConn.Read(buf)
	if err != nil {
		fmt.Println("receive socks handshake error:", err.Error())
		return
	}
	if bytes.Compare(buf[:len], []byte{05, 00}) != 0 {
		fmt.Println("non-standard socks5 handshake")
		return
	}
	stage = 3

	// 发送需要连接的地址
	addrBytes := []byte{05, 01, 00, 01}
	ipBytes := Int32ToBytes(StringIpToInt(configStruct.ServiceIp))
	servicePortNumber, _ := strconv.Atoi(configStruct.ServicePort)
	portBytes := Int16ToBytes(servicePortNumber)
	addrBytes = append(addrBytes, ipBytes...)
	addrBytes = append(addrBytes, portBytes...)
	_, err = remoteConn.Write(addrBytes)
	if err != nil {
		fmt.Println("send service data error:", err.Error())
		return
	}
	stage = 4

	// 接收socks服务端的远程连接结果
	buf = make([]byte, 1024)
	len, err = remoteConn.Read(buf)
	if err != nil {
		fmt.Println("socks connect error:", err.Error())
		return
	}
	if bytes.Compare(buf[:2], []byte{05, 00}) != 0 {
		fmt.Println("socks connect error")
		return
	}
	if len > 10 {
		_, err := localConn.Write(buf[10:len])
		if err != nil {
			fmt.Println("send first package error:", err.Error())
			return
		}
	}
	stage = 5
	fmt.Println(stage)

	var isFinished = false
	wg := sync.WaitGroup{}
	wg.Add(2)
	go handleBindCon(remoteConn, localConn, &wg, &isFinished)
	go handleBindCon(localConn, remoteConn, &wg, &isFinished)
	wg.Wait()
}

func handleBindCon(con1 net.Conn, con2 net.Conn, wg *sync.WaitGroup, isFinished *bool) {
	defer wg.Done()
	defer func() { *isFinished = true }()
	for {
		buf := make([]byte, 1024)
		err := con1.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err != nil {
			fmt.Println("con1 SetReadDeadline error:", err.Error())
			if *isFinished == true {
				return
			}
			continue
		}
		len, err := con1.Read(buf)
		if err != nil {
			if oe, ok := err.(*net.OpError); ok {
				isTimeout := oe.Timeout()
				if isTimeout {
					if *isFinished == true {
						return
					}
					//fmt.Println("read超时等待:", err.Error())
					continue
				}
			}
			if err.Error() == "EOF" {
				return
			}
			fmt.Println("receive package error:", err.Error())
			return
		}

		_, err = con2.Write(buf[:len])
		if err != nil {
			fmt.Println("send package error:", err.Error())
			return
		}
	}
}
