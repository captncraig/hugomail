package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

type config struct {
	GithubToken  string
	GithubUser   string
	GithubRepo   string
	Path         string
	MailgunToken string
	Authors      map[string]string
}

var conf config

var confFile = flag.String("c", "conf.json", "Location of config file")

var client *github.Client

func main() {
	flag.Parse()

	cDat, err := ioutil.ReadFile(*confFile)
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(cDat, &conf)
	if err != nil {
		log.Fatal(err)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: conf.GithubToken},
	)
	tc := oauth2.NewClient(oauth2.NoContext, ts)

	client = github.NewClient(tc)

	http.HandleFunc("/api/publish", mailgunHook)
	http.ListenAndServe(":5555", nil)
}

func logErr(w http.ResponseWriter, err error) {
	log.Println(err)
	http.Error(w, err.Error(), 500)
}

var tagsRegex = regexp.MustCompile(`^\[([^\]]+)\]`)

type attachments []struct {
	URL         string `json:"url"`
	ContentType string `json:"content-type"`
	Name        string `json:"name"`
	Size        int    `json:"size"`
}

func mailgunHook(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		logErr(w, err)
		return
	}
	body := r.FormValue("body-plain")
	sender := r.FormValue("sender")
	subject := r.FormValue("subject")

	sender, ok := conf.Authors[sender]
	if !ok {
		logErr(w, fmt.Errorf("Unknown Sender"))
		return
	}

	tags := []string{}
	tagsMatch := tagsRegex.FindStringSubmatch(subject)
	if tagsMatch != nil {
		subject = strings.TrimSpace(subject[len(tagsMatch[0]):])
		tags = strings.Split(tagsMatch[1], ",")
	}

	if jsn := r.FormValue("attachments"); jsn != "" {
		attaches := attachments{}
		if err := json.Unmarshal([]byte(jsn), &attaches); err == nil {
			for _, a := range attaches {
				if a.ContentType != "image/png" && a.ContentType != "image/jpg" && a.ContentType != "image/jpeg" && a.ContentType != "image/gif" {
					log.Printf("Unrecognized content type: %s. Skipping.", a.ContentType)
					continue
				}
				req, err := http.NewRequest("GET", a.URL, nil)
				if err != nil {
					log.Println("Error creating attachment request ", err)
					continue
				}
				req.SetBasicAuth("api", conf.MailgunToken)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Println("Error getting attachment ", err)
					continue
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					log.Printf("Unrecognized status code for attachment: %d. Skipping.", resp.StatusCode)
					continue
				}
				dat, err := ioutil.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					log.Println("Error getting attachment ", err)
					continue
				}
				b64 := base64.StdEncoding.EncodeToString(dat)
				log.Println(b64)
				body += fmt.Sprintf("\n\n![img](data:%s;base64,%s)", a.ContentType, b64)
				log.Println(body)
			}
		}
	}
	err = makePost(subject, body, sender, tags, time.Now())
	if err != nil {
		log.Println("Crap!", err)
	}
}

var msg = "Automatic Publish"

func makePost(title, body, author string, tags []string, timestamp time.Time) error {
	preamble := struct {
		Date          time.Time
		Title, Author string
		Tags          []string
	}{
		timestamp, title, author, tags,
	}
	dat, err := json.MarshalIndent(preamble, "", "  ")
	if err != nil {
		return err
	}
	content := string(dat) + "\n" + body

	fileName := conf.Path + fmt.Sprintf("/%s-%s.md", timestamp.Format("2006-01-02-1504"), strings.Replace(title, " ", "-", -1))
	opts := &github.RepositoryContentFileOptions{}
	opts.Content = []byte(content)
	opts.Message = &msg
	_, _, err = client.Repositories.CreateFile(conf.GithubUser, conf.GithubRepo, fileName, opts)
	return err
}
