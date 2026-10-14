// simulate is an offline test client. It never connects to a physical board.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
)

func main() {
	path := flag.String("config", "configs/local.example.json", "local test configuration")
	result := flag.String("sms-result", "delivered", "synthetic result: delivered, submitted, unknown, rejected")
	flag.Parse()
	if *result != "delivered" && *result != "submitted" && *result != "unknown" && *result != "rejected" {
		fmt.Fprintln(os.Stderr, "模拟结果选项无效")
		os.Exit(1)
	}
	c, e := config.Load(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, "测试配置无效")
		os.Exit(1)
	}
	host, _, _ := net.SplitHostPort(c.TCPAddr)
	if c.Environment != "local" || c.DeviceID != "SIM-TEST" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		fmt.Fprintln(os.Stderr, "模拟器只允许连接本机 SIM-TEST 测试环境")
		os.Exit(1)
	}
	conn, e := net.DialTimeout("tcp", c.TCPAddr, 3*time.Second)
	if e != nil {
		fmt.Fprintln(os.Stderr, "请先启动本地管理服务")
		os.Exit(1)
	}
	defer conn.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	codec := protocol.Codec{Mode: c.AESMode, Key: []byte(c.AESKey), IV: []byte(c.AESIV)}
	var lock sync.Mutex
	send := func(v map[string]any) error {
		v["devId"] = c.DeviceID
		v["msgTs"] = time.Now().Unix()
		p, e := json.Marshal(v)
		if e != nil {
			return e
		}
		p, e = codec.EncodeUpload(p)
		if e != nil {
			return e
		}
		p = append(p, protocol.Delimiter...)
		lock.Lock()
		defer lock.Unlock()
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		for len(p) > 0 {
			n, e := conn.Write(p)
			if e != nil {
				return e
			}
			p = p[n:]
		}
		return nil
	}
	if send(map[string]any{"type": 100}) != nil {
		return
	}
	reader := protocol.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	p, e := reader.Next()
	if e == nil && c.AESMode == 2 {
		p, e = codec.Open(p)
	}
	var cmd map[string]any
	if e != nil || json.Unmarshal(p, &cmd) != nil || cmd["cmd"] != "now" {
		fmt.Fprintln(os.Stderr, "握手未完成")
		return
	}
	conn.SetReadDeadline(time.Time{})
	if send(map[string]any{"type": 999, "code": 0, "tid": cmd["tid"]}) != nil {
		return
	}
	for slot := 1; slot <= 2; slot++ {
		if send(map[string]any{"type": 501, "slot": slot, "phNum": "TEST-SENDER", "smsTs": 1700000000 + slot, "smsBd": fmt.Sprintf("卡槽 %d 的合成短信。\n这条内容由本机模拟器生成，不是真实短信。", slot), "smsMD5": fmt.Sprintf("%016X", slot)}) != nil {
			return
		}
	}
	fmt.Println("模拟板子已接入：两条合成短信已上报，可在网页查询状态。停止模拟器后，页面会显示断开。")
	go func() {
		defer cancel()
		for {
			p, e := reader.Next()
			if e != nil {
				return
			}
			if c.AESMode == 2 {
				p, e = codec.Open(p)
				if e != nil {
					return
				}
			}
			var cmd map[string]any
			if json.Unmarshal(p, &cmd) != nil {
				return
			}
			if cmd["cmd"] == "stat" {
				if send(map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "hwVer": "SIMULATOR", "wifi": map[string]any{"mode": "ON", "ssid": "SYNTHETIC-WIFI", "dbm": 90}, "slotInfo": map[string]any{"slot1_sta": "OK", "slot2_sta": "OK", "sim1_dbm": "80", "sim2_dbm": "70", "sim1_plmn": "TEST", "sim2_plmn": "TEST", "sim1_iccId": "SYNTHETIC-ICC-0001", "sim2_iccId": "SYNTHETIC-ICC-0002"}}) != nil {
					return
				}
			}
			if cmd["cmd"] == "sendsms" {
				// No modem exists here. Count received frames without logging private content.
				fmt.Println("收到一条模拟外发指令；未连接 SIM 或运营商。")
				if *result == "unknown" {
					continue
				}
				if *result == "rejected" {
					if send(map[string]any{"type": 401, "subType": 16, "code": 200, "tid": cmd["tid"]}) != nil {
						return
					}
					continue
				}
				slot := 1
				if cmd["p1"] == "2" {
					slot = 2
				}
				if send(map[string]any{"type": 401, "subType": 16, "code": 0, "tid": cmd["tid"]}) != nil {
					return
				}
				if send(map[string]any{"type": 402, "subType": 16, "code": 0, "val": "7", "tid": cmd["tid"]}) != nil {
					return
				}
				if send(map[string]any{"type": 502, "slot": slot, "phNum": cmd["p2"], "smsBd": cmd["p3"], "tid": cmd["tid"], "smsSendMr": []int{7}}) != nil {
					return
				}
				if *result == "delivered" {
					if send(map[string]any{"type": 503, "slot": slot, "phNum": cmd["p2"], "smsSendMr": 7, "smsSt": 0, "smsScts": time.Now().Unix(), "smsDt": time.Now().Unix()}) != nil {
						return
					}
				}
			}

		}
	}()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if send(map[string]any{"type": 998}) != nil {
				return
			}
		}
	}
}
