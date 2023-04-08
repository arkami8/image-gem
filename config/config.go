package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
)

var (
	ServerPort         string
	CORSAllowedOrigins []string
)

type config struct {
	ServerPort         string   `json:"ServerPort"`
	CORSAllowedOrigins []string `json:"CORSAllowedOrigins"`
}

func ReadConfig() error {
	var config *config

	fmt.Println("Reading from config file...")

	file, err := ioutil.ReadFile("config.json")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(file))

	err = json.Unmarshal(file, &config)
	if err != nil {
		panic(err)
	}

	ServerPort = config.ServerPort
	if ServerPort != "" && !strings.HasPrefix(ServerPort, ":") {
		ServerPort = fmt.Sprintf(":%s", ServerPort)
	}

	CORSAllowedOrigins = config.CORSAllowedOrigins

	return nil
}
