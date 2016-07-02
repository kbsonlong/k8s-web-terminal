package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Host string
}

type ContainerResponse struct {
	items []Container
}

type ExecResponse struct {
	Id string
}

type Container struct {
	Id    string
	Names []string
}

func (client *Client) ListContainers() []Container {
	resp, err := http.Get(client.Host + "/containers/json")
	if err != nil {
		log.Fatal(err)
	}

	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	items := make([]Container, 0)
	json.Unmarshal(body, &items)
	for _, container := range items {
		fmt.Printf("%s %s\r\n", container.Id[:12], container.Names[0][1:])
	}
	return items
}

func (client *Client) CreateExec(id string, cmd string) (string, error) {
	var jsonBody = strings.NewReader(`{
		"AttachStdin": true,
		"AttachStdout": true,
		"AttachStderr": true,
		"DetachKeys": "ctrl-p,ctrl-q",
		"Tty": true,
		"Cmd": [
			"/bin/bash"	
		]
	}`)
	res, err := http.Post(client.Host+"/containers/"+id+"/exec", "application/json;charset=utf-8", jsonBody)

	if err != nil {
		return "", err
	}

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		fmt.Println(err)
		return "", err
	}

	var result ExecResponse
	json.Unmarshal(body, &result)
	return result.Id, nil
}

func (client *Client) ExecStart(id string, input chan []byte) (chan []byte, error) {

	execUrl, _ := url.Parse(client.Host + "/exec/" + id + "/start")
	return client.connect(execUrl, input)

}

func (client *Client) connect(url *url.URL, input chan []byte) (chan []byte, error) {
	output := make(chan []byte)

	req, _ := http.NewRequest("POST", url.String(), strings.NewReader(
		`{
			"Detach": false,
			"Tty": true
		}`))
	dial, err := net.Dial("tcp", url.Host)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	clientconn := httputil.NewClientConn(dial, nil)
	clientconn.Do(req)

	rwc, br := clientconn.Hijack()

	go func() {
		defer clientconn.Close()
		for {
			data := <-input
			rwc.Write(data)
		}
	}()

	go func() {
		defer rwc.Close()

		for {
			buf := make([]byte, 100)
			_, err := br.Read(buf)
			if err != nil {
				if err.Error() == "EOF" {
					output <- []byte("EOF")
					break
				}
				log.Println("Read Err: " + err.Error())
				break
			}

			output <- buf
			time.Sleep(500)
			buf = nil
		}

	}()
	return output, nil
}