package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"gopkg.in/mgo.v2"
)

type dotaresponse struct {
	Draft_timings []map[string]interface{} `json:"draft_timings"`
	Radiant_win   bool                     `json:"radiant_win"`
}

type dotamatchs struct {
	MatchId int `json:"match_id"`
	MMR     int `json:"avg_mmr"`
}

type MatchInfos struct {
	Picks []int
	Win   int
	ID    string
}

func main() {
	lambda.Start(HandleRequest)
}

func HandleRequest(ctx context.Context, data interface{}) (string, error) {

	address := os.Getenv("address")
	username := os.Getenv("username")
	password := os.Getenv("password")
	option := os.Getenv("option")
	ssl := os.Getenv("ssl")
	var s *mgo.Session
	var err error
	if ssl == "false" {
		s, err = mgo.Dial(fmt.Sprintf("mongodb://%s:%s@%s/%s", username, password, address, option))
		if err != nil {
			fmt.Printf("%s\n", err.Error())
			os.Exit(1)
		}
	} else {
		s, err = DialUsingSSL(address, option, username, password)
		if err != nil {
			fmt.Printf("%s\n", err.Error())
			os.Exit(1)
		}
	}
	defer s.Close()

	// Ensure the indexs (best effort operation)
	s.DB("opendota-infos").C("matchs").EnsureIndex(mgo.Index{
		Unique: true,
		Key:    []string{"id"},
	})

	matchs, err := getLast100Matches()
	if err != nil {
		fmt.Printf("%s\n", err.Error())
		os.Exit(1)
	}
	for i := 0; i < len(matchs); i++ {
		resp, _, err := downloadMatchAndReturnResult(matchs[i])
		if err != nil {
			log.Printf("%s\n", err.Error())
			os.Exit(1)
		}
		if len(resp.Picks) != 0 {
			s.DB("opendota-infos").C("matchs").Insert(resp)
		}
		// Don't flood opendota with too many request.
		time.Sleep(1 * time.Second)
	}

	return "", nil
}
func DialUsingSSL(addresses string, dboption string, username string, password string) (*mgo.Session, error) {
	listaddresses := make([]string, 0)
	for _, str := range strings.Split(addresses, ",") {
		if str != "" {
			listaddresses = append(listaddresses, str)
		}
	}
	dboptions := strings.Split(dboption, "=")
	if len(dboption) < 2 {
		return nil, fmt.Errorf("can not found authSource keyword in order to permit SSL connection, aborting")
	}
	tlsConfig := &tls.Config{}
	dialInfo := &mgo.DialInfo{
		Addrs:    listaddresses,
		Database: dboptions[1],
		Username: username,
		Password: password,
	}

	dialInfo.DialServer = func(addr *mgo.ServerAddr) (net.Conn, error) {
		conn, err := tls.Dial("tcp", addr.String(), tlsConfig)
		return conn, err
	}
	session, err := mgo.DialWithInfo(dialInfo)
	if err != nil {
		return nil, err
	}
	session.EnsureSafe(&mgo.Safe{
		W:     1,
		FSync: false,
	})
	return session, nil
}

func getLast100Matches() ([]string, error) {
	resp, err := http.Get("https://api.opendota.com/api/proMatches")
	if err != nil {
		return nil, fmt.Errorf("error while downloading data: %s\n", err.Error())
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	var response []dotamatchs
	json.Unmarshal(body, &response)
	var matchs []string
	for i := 0; i < len(response); i++ {
		id := response[i].MatchId
		matchs = append(matchs, strconv.Itoa(id))
	}
	return matchs, nil
}

func downloadMatchAndReturnResult(matchID string) (*MatchInfos, map[string]interface{}, error) {
	resp, err := http.Get("https://api.opendota.com/api/matches/" + matchID)
	if err != nil {
		return nil, nil, fmt.Errorf("error while downloading data: %s\n", err.Error())
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	var codec dotaresponse
	json.Unmarshal(body, &codec)
	rawResponse := make(map[string]interface{})
	json.Unmarshal(body, &rawResponse)
	var ris MatchInfos
	var teamZeroIsRadian bool
	for i := 0; i < len(codec.Draft_timings); i++ {
		if codec.Draft_timings[i]["pick"] == true {
			conv, ok := codec.Draft_timings[i]["hero_id"].(float64)
			if !ok {
				return nil, nil, fmt.Errorf("Can't convert hero id to int. %d", codec.Draft_timings[i]["hero_id"])
			}
			ris.Picks = append(ris.Picks, int(conv))
			var playerslot float64
			if i == 6 {
				playerslot = codec.Draft_timings[i]["player_slot"].(float64)
			}
			if i == 6 && playerslot < 5.0 {
				fmt.Printf("setting teamZeroIsRadian to true\n")
				teamZeroIsRadian = true
			}
		}
	}

	var teamzerowin bool
	if teamZeroIsRadian {
		teamzerowin = codec.Radiant_win
	} else {
		teamzerowin = !codec.Radiant_win
	}

	if teamzerowin == false {
		ris.Win = 1
	}

	ris.ID = matchID
	return &ris, rawResponse, nil
}
