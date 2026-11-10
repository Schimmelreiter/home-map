package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// liveConn haelt die MQTT-Verbindung zu Zigbee2MQTT und reicht gebuendelt weiter:
//   - die Geraeteliste (bridge/devices)
//   - Verfuegbarkeit online/offline (<name>/availability)
//   - die zuletzt gemeldete LQI je Geraet (linkquality in den Zustandsmeldungen)
//   - die Antwort auf eine angeforderte Netzwerkkarte
type liveConn struct {
	emit func(any)

	mu     sync.Mutex
	cl     mqtt.Client
	base   string
	avail  map[string]string
	lqi    map[string][2]any
	stop   chan struct{}
	gen    int
	mapGen int
}

func newLiveConn(emit func(any)) *liveConn {
	return &liveConn{emit: emit}
}

func (l *liveConn) status(connected, busy bool, text string) {
	l.emit(map[string]any{"type": "status", "connected": connected, "busy": busy, "text": text})
}

func (l *liveConn) connect(host string, port int, user, pass, base string) {
	l.disconnect()
	l.mu.Lock()
	l.gen++
	gen := l.gen
	l.base = strings.TrimRight(base, "/")
	l.avail, l.lqi = map[string]string{}, map[string][2]any{}
	l.stop = make(chan struct{})
	l.mu.Unlock()

	addr := fmt.Sprintf("tcp://%s:%d", host, port)
	l.status(false, true, "verbinde mit "+host+" …")
	opts := mqtt.NewClientOptions().AddBroker(addr).
		SetClientID(fmt.Sprintf("home-map-%d", time.Now().UnixNano()%1e8)).
		SetUsername(user).SetPassword(pass).
		SetConnectTimeout(8 * time.Second).SetKeepAlive(30 * time.Second).
		SetAutoReconnect(true).SetConnectRetry(false).SetOrderMatters(false).SetCleanSession(true)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		if !l.current(gen) {
			return
		}
		l.status(true, false, "verbunden mit "+host)
		c.Subscribe(l.base+"/#", 0, func(_ mqtt.Client, m mqtt.Message) { l.onMessage(gen, m) })
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		if l.current(gen) {
			l.status(false, true, "Verbindung verloren, verbinde neu … ("+err.Error()+")")
		}
	})
	cl := mqtt.NewClient(opts)
	l.mu.Lock()
	l.cl = cl
	l.mu.Unlock()

	tok := cl.Connect()
	if !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		err := tok.Error()
		if err == nil {
			err = errors.New("keine Antwort vom Broker")
		}
		if l.current(gen) {
			l.status(false, false, "nicht verbunden: "+deutsch(err))
			cl.Disconnect(0)
		}
		return
	}
	go l.flushLoop(gen)
}

func (l *liveConn) current(gen int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return gen == l.gen
}

func (l *liveConn) disconnect() {
	l.mu.Lock()
	cl, stop := l.cl, l.stop
	l.cl, l.stop = nil, nil
	l.gen++
	l.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if cl != nil {
		cl.Disconnect(250)
		l.status(false, false, "getrennt")
	}
}

func (l *liveConn) onMessage(gen int, m mqtt.Message) {
	if !l.current(gen) {
		return
	}
	topic := strings.TrimPrefix(m.Topic(), l.base+"/")
	p := m.Payload()
	switch {
	case topic == "bridge/devices":
		var list []map[string]any
		if json.Unmarshal(p, &list) == nil {
			slim := make([]map[string]any, 0, len(list))
			for _, d := range list {
				slim = append(slim, map[string]any{"ieee_address": d["ieee_address"], "friendly_name": d["friendly_name"], "type": d["type"]})
			}
			l.emit(map[string]any{"type": "devices", "list": slim})
		}
	case topic == "bridge/response/networkmap":
		var r struct {
			Status string          `json:"status"`
			Error  string          `json:"error"`
			Data   json.RawMessage `json:"data"`
		}
		if json.Unmarshal(p, &r) != nil {
			return
		}
		if r.Status != "ok" {
			l.emit(map[string]any{"type": "networkmap", "error": r.Error})
			return
		}
		l.emit(map[string]any{"type": "networkmap", "raw": map[string]any{"data": r.Data}})
	case strings.HasPrefix(topic, "bridge/"):
		// sonstige Bruecken-Meldungen interessieren hier nicht
	case strings.HasSuffix(topic, "/availability"):
		name := strings.TrimSuffix(topic, "/availability")
		st := strings.Trim(string(p), `" `)
		var a struct {
			State string `json:"state"`
		}
		if json.Unmarshal(p, &a) == nil && a.State != "" {
			st = a.State
		}
		l.mu.Lock()
		if l.avail != nil {
			l.avail[name] = st
		}
		l.mu.Unlock()
	case !strings.Contains(topic, "/"):
		var s struct {
			LQI *int `json:"linkquality"`
		}
		if json.Unmarshal(p, &s) == nil && s.LQI != nil {
			l.mu.Lock()
			if l.lqi != nil {
				l.lqi[topic] = [2]any{*s.LQI, time.Now().UnixMilli()}
			}
			l.mu.Unlock()
		}
	}
}

// flushLoop gibt die gesammelten Live-Werte hoechstens einmal je Sekunde weiter.
func (l *liveConn) flushLoop(gen int) {
	l.mu.Lock()
	stop := l.stop
	l.mu.Unlock()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.mu.Lock()
			if gen != l.gen {
				l.mu.Unlock()
				return
			}
			av, lq := l.avail, l.lqi
			l.avail, l.lqi = map[string]string{}, map[string][2]any{}
			l.mu.Unlock()
			if len(av) > 0 || len(lq) > 0 {
				l.emit(map[string]any{"type": "live", "avail": av, "lqi": lq})
			}
		}
	}
}

func (l *liveConn) requestNetworkMap(routes bool) error {
	l.mu.Lock()
	cl, base := l.cl, l.base
	l.mu.Unlock()
	if cl == nil || !cl.IsConnected() {
		return errors.New("nicht verbunden")
	}
	req, _ := json.Marshal(map[string]any{"type": "raw", "routes": routes})
	tok := cl.Publish(base+"/bridge/request/networkmap", 0, false, req)
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}

func deutsch(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "not Authorized"), strings.Contains(s, "bad user name or password"):
		return "Benutzer oder Passwort falsch"
	case strings.Contains(s, "no such host"):
		return "Rechnername unbekannt"
	case strings.Contains(s, "refused"):
		return "Verbindung abgelehnt (Port richtig?)"
	case strings.Contains(s, "timeout"), strings.Contains(s, "i/o timeout"):
		return "keine Antwort (Rechner erreichbar?)"
	}
	return s
}
