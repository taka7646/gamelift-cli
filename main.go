package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/gamelift"
	"github.com/ktr0731/go-fuzzyfinder"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli"
)

const SelectLimit = 10

var version = "0.0.1"
var hash = "unknown"
var isWindows = runtime.GOOS == "windows"

var globalOptions []string
var fleet FleetAttribute
var instance FleetInstance

type FleetAttribute struct {
	FleetID                        string
	FleetArn                       string
	FleetType                      string
	InstanceTYpe                   string
	Name                           string
	CrateionTime                   float64
	Status                         string
	LogPaths                       []string
	NewGameSessionProtectionPolicy string
	OperatingSystem                string
	MetricGroups                   []string
}

type FleetAttributes struct {
	FleetAttributes []FleetAttribute
}

type FleetInstance struct {
	FleetID         string
	InstanceID      string
	IPAddress       string
	OperatingSystem string
	Type            string
	Status          string
	CreationTime    float64
}
type FleetInstances struct {
	Instances []FleetInstance
}

type Credential struct {
	UserName string
	Secret   string
}
type InstanceAccess struct {
	FleetID         string
	InstanceID      string
	IPAddress       string
	OperatingSystem string
	Credentials     Credential
}
type OutInstanceAccess struct {
	InstanceAccess InstanceAccess
}

type GameProperty struct {
	Key   string
	Value string
}

var sessionStatusOrder = map[string]int{
	"ACTIVE":      0,
	"ACTIVATING":  1,
	"TERMINATING": 2,
	"TERMINATED":  3,
}

type GameSession struct {
	GameSessionID               string
	Name                        string
	FleetID                     string
	CreationTime                float64
	TerminationTime             float64
	CurrentPlayerSessionCount   int
	MaximumPlayerSessionCount   int
	Status                      string // ACTIVE, TERMINATED, ACTIVATING, TERMINATING
	GameProperties              []GameProperty
	IPAddress                   string
	Port                        int
	PlayerSessionCreationPolicy string
}

type OutGameSession struct {
	GameSessions []GameSession
}

func toTimeStr(timestamp float64) string {
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	t := time.Unix(int64(timestamp), int64(timestamp*1000000)%1000000).In(jst)
	return t.Format("2006-01-02 15:04:05")
}

func appendOptions(base []string, args ...string) []string {
	var opt = make([]string, len(base)+len(args))
	copy(opt, base)
	copy(opt[len(base):], args)
	return opt
}

func command(name string, globalOptions []string, options ...string) *exec.Cmd {
	opt := appendOptions(globalOptions, options...)
	cmd := exec.Command(name, opt...)
	return cmd
}

func commandRun(name string, globalOptions []string, options ...string) ([]byte, error) {
	cmd := command(name, globalOptions, options...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func selectFleetCmd() (string, error) {
	cmd := command("aws", globalOptions, "gamelift", "describe-fleet-attributes")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	// // out := io.MultiWriter(&buf, os.Stdout)
	// cmd.Stdout = out
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	var fleetAttr FleetAttributes
	//	var fleetAttr FleetAttributes
	var fleetID string
	json.Unmarshal(buf.Bytes(), &fleetAttr)
	index, err := fuzzyfinder.Find(fleetAttr.FleetAttributes, func(i int) string {
		fleet := fleetAttr.FleetAttributes[i]
		return fleet.Name
	}, fuzzyfinder.WithPromptString("Select Fleet > "))
	if err != nil {
		return "", err
	}
	fleet = fleetAttr.FleetAttributes[index]
	fleetID = fleet.FleetID
	return fleetID, nil
}

// aws-sdk-goでの実装　MFAトークンを共有できないため、毎回入力が必要になってしまう・・・
// selectFleet フリートIDを取得
func selectFleet() (string, error) {
	cfg := aws.NewConfig()
	options := session.Options{
		Config:  *cfg,
		Profile: "bfxx",
		// 標準入力からMFAトークンを入力する
		AssumeRoleTokenProvider: stscreds.StdinTokenProvider,
		// configを自動でロードする
		SharedConfigState: session.SharedConfigEnable,
	}
	sess := session.Must(session.NewSessionWithOptions(options))
	client := gamelift.New(sess)
	in := gamelift.DescribeFleetAttributesInput{}
	out, err := client.DescribeFleetAttributes(&in)
	if err != nil {
		return "", err
	}
	for i, f := range out.FleetAttributes {
		fmt.Printf("[%d] %s\n", i, *f.Name)
	}
	var fleetID string
	for {
		fmt.Printf("対象のFleetを選択してください[0-%d]", len(out.FleetAttributes)-1)
		var index int
		_, err = fmt.Scanf("%d", &index)
		if err != nil {
			return "", err
		}
		if index >= 0 && index < len(out.FleetAttributes) {
			fleetID = *out.FleetAttributes[index].FleetId
			break
		}
	}
	return fleetID, nil
}

func selectGameSession(fleetID string) (*GameSession, error) {
	bytes, err := commandRun("aws", globalOptions, "gamelift", "describe-game-sessions", "--fleet-id", fleetID)
	if err != nil {
		return nil, err
	}
	var sessions OutGameSession
	//fmt.Printf("sessions  %s\n", string(bytes))
	json.Unmarshal(bytes, &sessions)
	sort.Slice(sessions.GameSessions, func(i, j int) bool {
		l := sessions.GameSessions[i]
		r := sessions.GameSessions[j]
		if sessionStatusOrder[l.Status] == sessionStatusOrder[r.Status] {
			// 作成日時が新しい順
			return l.CreationTime > r.CreationTime
		}
		return sessionStatusOrder[l.Status] < sessionStatusOrder[r.Status]
	})

	count := len(sessions.GameSessions)
	if count == 0 {
		return nil, nil
	}
	if count > SelectLimit {
		sessions.GameSessions = sessions.GameSessions[0:SelectLimit]
		count = SelectLimit
	}
	activeCount := 0
	for _, v := range sessions.GameSessions {
		if v.Status == "ACTIVE" || v.Status == "ACTIVATING" {
			activeCount++
		}
	}
	if activeCount == 1 {
		return &sessions.GameSessions[0], nil
	}

	index, err := fuzzyfinder.Find(sessions.GameSessions, func(i int) string {
		session := &sessions.GameSessions[i]
		return fmt.Sprintf("%s [%s]", session.Name, session.Status)
	}, fuzzyfinder.WithPromptString("Select Fleet > "))
	if err != nil {
		return nil, err
	}
	session := &sessions.GameSessions[index]
	return session, nil
}

func selectInstance(fleetID string, session *GameSession) (string, error) {
	bytes, err := commandRun("aws", globalOptions, "gamelift", "describe-instances", "--fleet-id", fleetID)
	if err != nil {
		return "", err
	}
	var instances FleetInstances
	json.Unmarshal(bytes, &instances)
	//fmt.Printf("%#v", instances)
	if len(instances.Instances) == 0 {
		return "", fmt.Errorf("インスタンスがありません %s", fleetID)
	} else if session != nil {
		for _, ins := range instances.Instances {
			if session.IPAddress == ins.IPAddress {
				instance = ins
				break
			}
		}
		if instance.FleetID == "" {
			return "", fmt.Errorf("対象のインスタンスが見つかりません %s", session.IPAddress)
		}
	} else if len(instances.Instances) == 1 {
		instance = instances.Instances[0]
	} else {
		for i, ins := range instances.Instances {
			fmt.Printf("[%d] %s %s\n", i, ins.IPAddress, ins.InstanceID)
		}
		var count = len(instances.Instances)
		for {
			fmt.Printf("対象のインスタンスを選択してください[0-%d]", count-1)
			var index int
			_, err = fmt.Scanf("%d", &index)
			if err != nil {
				return "", err
			}
			if index >= 0 && index < count {
				instance = instances.Instances[index]
				break
			}
		}
	}
	return instance.InstanceID, nil
}

func getInstanceAccess(fleetID string, instanceID string, configPath string) (string, error) {
	bytes, err := commandRun("aws", globalOptions, "gamelift", "get-instance-access", "--fleet-id", fleetID, "--instance-id", instanceID)
	if err != nil {
		return "", err
	}
	var out OutInstanceAccess
	json.Unmarshal(bytes, &out)
	pemName := "./tmp_gamelift.pem"
	if isWindows {
		// Windowsは秘密鍵を $HOME/.ssh/ 以下に置かないとパーミッションエラーになる
		baseDir, err := homedir.Dir()
		if err != nil {
			baseDir = "."
		} else {
			baseDir = filepath.Join(baseDir, ".ssh")
		}
		os.MkdirAll(baseDir, 0700)
		pemName = filepath.Join(baseDir, "tmp_gamelift.pem")
	}
	err = ioutil.WriteFile(pemName, []byte(out.InstanceAccess.Credentials.Secret), 0600)
	if err != nil {
		return "", err
	}
	config := `
Host gamelift
	User %s
	HostName %s
	IdentityFile %s
`
	config = fmt.Sprintf(config, out.InstanceAccess.Credentials.UserName, out.InstanceAccess.IPAddress, pemName)
	err = ioutil.WriteFile(configPath, []byte(config), 0600)
	if err != nil {
		return "", err
	}
	return out.InstanceAccess.Credentials.UserName, nil
}

func tailLog(session *GameSession, configPath string, fleet *FleetAttribute) error {
	options := []string{"-F", configPath, "gamelift"}
	bytes, err := commandRun("ssh", options, "sudo", "lsof", fmt.Sprintf("-i:%d", session.Port), "-P", "-t")
	if err != nil {
		fmt.Println("ssh error " + err.Error())
		return err
	}
	processID := strings.Trim(string(bytes), " \n")
	d := time.Now().UTC()
	path := filepath.Join(fleet.LogPaths[0], processID, "server.log."+d.Format("2006-01-02-15"))
	fmt.Printf("tail -f %s\n", path)
	cmd := command("ssh", options, "tail", "-f", path)
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		fmt.Println("ssh error " + err.Error())
		return err
	}
	return nil
}

// LogProc ログTail
func LogProc(c *cli.Context) error {
	fleetID, err := selectFleetCmd()
	if err != nil {
		return err
	}
	fmt.Printf("fleet-id: %s\n", fleetID)
	session, err := selectGameSession(fleetID)
	if err != nil {
		return err
	}
	fmt.Printf("gamesession: %s\n", session.Name)
	instanceID, err := selectInstance(fleetID, session)
	if err != nil {
		return err
	}
	configPath := "tmp_ssh.config"
	_, err = getInstanceAccess(fleetID, instanceID, configPath)
	if err != nil {
		return err
	}
	err = tailLog(session, configPath, &fleet)
	if err != nil {
		return err
	}
	return nil
}

// SSHProc SSH
func SSHProc(c *cli.Context) error {
	fleetID, err := selectFleetCmd()
	if err != nil {
		return err
	}
	fmt.Printf("fleet-id: %s\n", fleetID)
	session, err := selectGameSession(fleetID)
	if err != nil {
		return err
	}
	fmt.Printf("gamesession: %s\n", session.Name)
	instanceID, err := selectInstance(fleetID, session)
	if err != nil {
		return err
	}
	configPath := "tmp_ssh.config"
	_, err = getInstanceAccess(fleetID, instanceID, configPath)
	if err != nil {
		return err
	}

	path, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	args := []string{"-J", "gamelift", "-F", configPath}
	env := os.Environ()
	err = syscall.Exec(path, args, env)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	globalOptions = []string{"--output", "json"}
	app := cli.NewApp()
	app.Name = "gamelift-cli"
	app.Usage = "Gamelift クライアント"
	app.Version = fmt.Sprintf("%s (%s)", version, hash)
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "profile",
			Usage: "クレデンシャルProfile名",
			Value: "",
		},
	}
	app.Before = func(c *cli.Context) error {
		profile := c.GlobalString("profile")
		if len(profile) > 0 {
			globalOptions = append(globalOptions, "--profile", profile)
		}
		return nil
	}
	app.Commands = []cli.Command{
		{
			Name:   "log",
			Usage:  "ログをTailする",
			Action: LogProc,
		},
		{
			Name:   "ssh",
			Usage:  "インスタンスにSSHログインする",
			Action: SSHProc,
		},
	}
	app.Run(os.Args)
}
