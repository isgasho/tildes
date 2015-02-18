package main

import (
	"encoding/json"
	"strings"
	"bufio"
	"os"
	"io/ioutil"
	"os/exec"
	"fmt"
	"regexp"
	"net/http"
	"flag"
	"time"
	"text/template"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/thebaer/geo"
	"github.com/thebaer/tildes/store"
)

const (
	locDataJSON = "/home/bear/public_html/where.json"
)

var (
	hashKey = "~~~"
)

func main() {
	// Get arguments
	outFilePtr := flag.String("f", "where", "Outputted HTML filename (without .html)")
	geocodeAPIKeyPtr := flag.String("k", "", "Google Geocoding API key")
	hashKeyPtr := flag.String("s", "", "Secret for hashing usernames")
	flag.Parse()

	// Set globals
	hashKey = *hashKeyPtr

	// Get online users with `who`
	users := who()

	// Fetch user locations based on IP address
	for i := range users {
		getGeo(&users[i])
		getFuzzyCoords(&users[i], *geocodeAPIKeyPtr)
	}

	// Write user coord data
	cacheUserLocations(&users)

	// Generate page
	generate(users, *outFilePtr)
}

type user struct {
	Name string `json:"name"`
	IP string `json:"ip"`
	Region string `json:"region"`
	Country string `json:"country"`
	CurrentTime string `json:"current_time"`
	Latitude float64 `json:"lat"`
	Longitude float64 `json:"lng"`
}

type publicUser struct {
	Name string `json:"name"`
	Region string `json:"region"`
	Country string `json:"country"`
	Latitude float64 `json:"lat"`
	Longitude float64 `json:"lng"`
}

var ipRegex = regexp.MustCompile("(([0-9]{1,3}[.-]){3}[0-9]{1,3})")

func who() []user {
	fmt.Println("who --ips")

	cmd := exec.Command("who", "--ips")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println(err)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println(err)
	}

	ips := make(map[string]string)

	r := bufio.NewReader(stdout)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lineParts := strings.Split(scanner.Text(), " ")
		name := lineParts[0]

		fmt.Println(name)

		// Extract IP address
		ipMatch := ipRegex.FindAllString(scanner.Text(), 1)
		if len(ipMatch) == 0 {
			continue
		}

		// Normalize any host names with dashes
		newIp := strings.Replace(ipMatch[0], "-", ".", -1)

		ips[newIp] = name
	}

	users := make([]user, len(ips))
	i := 0
	for ip, name := range ips {
		users[i] = user{Name: name, IP: ip}
		i++
	}

	return users
}

func getTimeInZone(tz string) string {
	cmd := exec.Command("date", "+%A %H:%M")
	cmd.Env = append(cmd.Env, fmt.Sprintf("TZ=%s", tz))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println(err)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println(err)
	}

	r := bufio.NewReader(stdout)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		return scanner.Text()
	}
	return ""
}

func getGeo(u *user) {
	fmt.Printf("Fetching %s location...\n", u.Name)

	response, err := http.Get(fmt.Sprintf("https://freegeoip.net/json/%s", u.IP))
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("%s", err)
			os.Exit(1)
		}

		var dat map[string]interface{}

		if err := json.Unmarshal(contents, &dat); err != nil {
			fmt.Println(err)
			return
		}
		region := dat["region_name"].(string)
		country := dat["country_name"].(string)

		u.CurrentTime = getTimeInZone(dat["time_zone"].(string))
		u.Region = region
		u.Country = country
	}
}

func computeHmac256(message string) string {
    key := []byte(hashKey)
    h := hmac.New(sha256.New, key)
    h.Write([]byte(message))
    return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func getFuzzyCoords(u *user, apiKey string) {
	fmt.Printf("Fetching %s fuzzy coordinates...\n", u.Name)

	loc := prettyLocation(u.Region, u.Country)
	addr, err := geo.Geocode(loc, apiKey)

	if err != nil {
		fmt.Println(err)
		return
	}

	u.Latitude = addr.Lat
	u.Longitude = addr.Lng
}

func cacheUserLocations(users *[]user) {
	// Read user data
	res := &map[string]publicUser{}
	if err := json.Unmarshal(store.ReadData(locDataJSON), &res); err != nil {
		fmt.Println(err)
	}

	// Update user data
	for i := range *users {
		u := (*users)[i]
		(*res)[computeHmac256(u.Name)] = publicUser{Name: u.Name, Region: u.Region, Country: u.Country, Latitude: u.Latitude, Longitude: u.Longitude}
	}

	// Write user data
	json, _ := json.Marshal(res)
	store.WriteData(locDataJSON, json)
}

func prettyLocation(region, country string) string {
	if region != "" {
		return fmt.Sprintf("%s, %s", region, country)
	}
	return country
}

type page struct {
	Users []user
	Updated string
	UpdatedForHumans string
}

func generate(users []user, outputFile string) {
	fmt.Println("Generating page.")

	f, err := os.Create(os.Getenv("HOME") + "/public_html/" + strings.ToLower(outputFile) + ".html")
	if err != nil {
		panic(err)
	}
	
	defer f.Close()

	funcMap := template.FuncMap {
		"Location": prettyLocation,
	}
	
	w := bufio.NewWriter(f)
	template, err := template.New("").Funcs(funcMap).ParseFiles("../templates/where.html")
	if err != nil {
		panic(err)
	}

	// Extra page data
	curTime := time.Now().UTC()
	updatedReadable := curTime.Format(time.RFC1123)
	updated := curTime.Format(time.RFC3339)

	// Generate the page
	p := &page{Users: users, UpdatedForHumans: updatedReadable, Updated: updated}
	template.ExecuteTemplate(w, "where", p)
	w.Flush()

	fmt.Println("DONE!")
}
