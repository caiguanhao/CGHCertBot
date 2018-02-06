package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
)

func check(host string) (expire time.Time, err error) {
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	var conn *tls.Conn
	dialer := &net.Dialer{
		Timeout: time.Duration(3) * time.Second,
	}
	conn, err = tls.DialWithDialer(dialer, "tcp", host, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for _, chain := range conn.ConnectionState().VerifiedChains {
		for _, cert := range chain {
			if expire.IsZero() || cert.NotAfter.Before(expire) {
				expire = cert.NotAfter
			}
		}
	}
	return
}

func sanitize(input string) string {
	if strings.HasPrefix(input, "http") {
		u, err := url.Parse(input)
		if err == nil {
			return u.Host
		}
	}
	return input
}

const (
	NOTHING = 0
	CREATE  = 1
	READ    = 2
	DELETE  = 3
)

var (
	delCmd = regexp.MustCompile("^/(?:d(?:el(?:ete|)|)|r(?:em(?:ove|)|))\\s*")
)

func reply(input string) (output string, expiry time.Time, action int) {
	if input == "/start" {
		output = `Give me website hostname and I'll tell you the expiry date of the certificates.

If I don't reply you within 5 seconds, that means I'm offline.`
		return
	}

	if input == "/list" {
		action = READ
		return
	}

	if delCmd.MatchString(input) {
		output = delCmd.ReplaceAllString(input, "")
		action = DELETE
		return
	}

	var err error
	expiry, err = check(input)
	if err == nil {
		diff := expiry.Sub(time.Now())
		days := int(diff.Hours() / 24)
		if days > 1 {
			output = fmt.Sprintf("%s will expire in %d days", input, days)
		} else if days > 0 {
			output = fmt.Sprintf("%s will expire tomorrow", input)
		} else if days == 0 {
			output = fmt.Sprintf("%s expires today", input)
		} else if days == -1 {
			output = fmt.Sprintf("%s expired yesterday", input)
		} else {
			output = fmt.Sprintf("%s expired %d days ago", input, -1*days)
		}
		action = CREATE
		return
	}

	switch err := err.(type) {
	case net.Error:
		if err.Timeout() {
			output = fmt.Sprintf("Timed out connecting %s", input)
			return
		}
	}

	if strings.Contains(err.Error(), "no such host") {
		output = "No such host."
		return
	}

	if strings.Contains(err.Error(), "no route to host") {
		output = "I don't understand what you typed."
		return
	}

	log.Println(err)
	output = "Something went wrong."
	return
}

type hosts map[int][]string

func (h hosts) Add(userId int, host string) {
	for _, _h := range h[userId] {
		if _h == host {
			return
		}
	}
	botdata.Hosts[userId] = append(botdata.Hosts[userId], host)
}

func (h hosts) Remove(userId int, host string) {
	for i := len((h)[userId]) - 1; i > -1; i-- {
		if (h)[userId][i] == host {
			(h)[userId] = append((h)[userId][:i], (h)[userId][i+1:]...)
		}
	}
}

func (h hosts) SummaryForUser(userId int) string {
	var messages expiryMessages
	var wg sync.WaitGroup
	wg.Add(len(h[userId]))
	for _, host := range h[userId] {
		go func(host string) {
			output, expiry, _ := reply(host)
			messages = append(messages, expiryMessage{expiry, output})
			wg.Done()
		}(host)
	}
	wg.Wait()
	if len(messages) == 0 {
		return "Nothing to show."
	}
	return messages.Sort().Join("\n")
}

type expiryMessage struct {
	Expiry  time.Time
	Message string
}
type expiryMessages []expiryMessage

func (eMs expiryMessages) Join(sep string) string {
	msgs := make([]string, len(eMs))
	for _, eM := range eMs {
		msgs = append(msgs, eM.Message)
	}
	return strings.Join(msgs, sep)
}

func (eMs expiryMessages) Sort() expiryMessages {
	sort.SliceStable(eMs, func(i, j int) bool {
		return eMs[i].Expiry.Before(eMs[j].Expiry)
	})
	return eMs
}

var botdata struct {
	Hosts hosts `json:"hosts"`
}

func read() error {
	defer func() {
		if botdata.Hosts == nil {
			botdata.Hosts = hosts{}
		}
	}()
	botdata.Hosts = nil
	b, err := ioutil.ReadFile("botdata.json")
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &botdata)
}

func write() error {
	b, err := json.MarshalIndent(botdata, "", "  ")
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile("botdata.json", b, 0644); err != nil {
		return err
	}
	return nil
}

var isSummarize bool

func init() {
	read()
	flag.BoolVar(&isSummarize, "summarize", false, "send summarized messages to users and exit")
}

func main() {
	flag.Parse()

	bot, err := tgbotapi.NewBotAPI(os.Getenv("BOTAPI"))
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false

	if isSummarize {
		for userId := range botdata.Hosts {
			bot.Send(tgbotapi.NewMessage(int64(userId), botdata.Hosts.SummaryForUser(userId)))
		}
		return
	}

	log.Printf("Started %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Panic(err)
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		go func(userId int, input string) {
			output, _, action := reply(input)
			switch action {
			case CREATE:
				botdata.Hosts.Add(userId, input)
				write()
			case READ:
				read()
				output = botdata.Hosts.SummaryForUser(userId)
			case DELETE:
				botdata.Hosts.Remove(userId, output)
				write()
				output = botdata.Hosts.SummaryForUser(userId)
			}
			bot.Send(tgbotapi.NewMessage(int64(userId), output))
		}(update.Message.From.ID, sanitize(update.Message.Text))
	}
}
