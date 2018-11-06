// Copyright (c) 2018, Oracle and/or its affiliates. All rights reserved.

package httpreplay

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Scenario format versions
const (
	scenarioFormatV1 = 1
)

var (
	interactionNotFound = errors.New("Requested interaction not found ")
)

// Request represents a client request as recorded in the
// scenario file
type Request struct {
	// Body of request
	Body string `yaml:"body"`

	// BodyParsed is parsed from body json
	BodyParsed interface{} `yaml:"bodyParsed"`

	// Form values
	Form url.Values `yaml:"form"`

	// Request headers
	Headers http.Header `yaml:"headers"`

	// Request URL
	URL string `yaml:"url"`

	// Request method
	Method string `yaml:"method"`
}

// Response represents a server response as recorded in the
// scenario file
type Response struct {
	// Body of responsen
	Body string `yaml:"body"`

	// BodyParsed is parsed from body json
	BodyParsed interface{} `yaml:"bodyParsed"`

	// Response headers
	Headers http.Header `yaml:"headers"`

	// Response status message
	Status string `yaml:"status"`

	// Response status code
	Code int `yaml:"code"`

	// Response duration (something like "100ms" or "10s")
	Duration string `yaml:"duration"`
}

// Interaction type contains a pair of request/response for a
// single HTTP interaction between a client and a server
type Interaction struct {
	Index    int `yaml:"index"`
	Uses     int `yaml:"uses"`
	Request  `yaml:"request"`
	Response `yaml:"response"`
}

type Matcher func(int, *Request, *Request) bool

func matcher(n int, r *Request, i *Request) bool {
	rUrl := stripQuery(r.URL)
	iUrl := stripQuery(i.URL)
	if r.Method != i.Method {
		return false
	}
	if rUrl != iUrl {
		return false
	}
	return true
}

func stripQuery(url string) string {
	l := strings.Split(url, "?")
	return l[0]
}

type Interactions []Interaction

// Scenario type
type Scenario struct {
	// Name of the scenario
	Name string `yaml:"-"`

	// File name of the scenario as written on disk
	File string `yaml:"-"`

	// Scenario format version
	Version int `yaml:"version"`

	// Mutex to lock accessing Interactions. omitempty is set
	// to prevent the mutex appearing in the recorded YAML.
	Mu sync.RWMutex `yaml:"mu,omitempty"`
	// Interactions between client and server
	Interactions       `yaml:"interactions"`
	sortedInteractions Interactions `yaml:"-"`

	// Matches actual request with interaction requests.
	Matcher `yaml:"-"`
}

// Implementations of sort.Interface to give us different orderings.

type byUsage Interactions

func (a byUsage) Len() int {
	return len(a)
}
func (a byUsage) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}
func (a byUsage) Less(i, j int) bool {
	if a[i].Uses != a[j].Uses {
		return a[i].Uses < a[j].Uses
	}
	return a[i].Index < a[j].Index
}

type byIndex Interactions

func (a byIndex) Len() int {
	return len(a)
}
func (a byIndex) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}
func (a byIndex) Less(i, j int) bool {
	return a[i].Index < a[j].Index
}

// NewScenario creates a new empty Scenario
func NewScenario(name string) *Scenario {
	s := &Scenario{
		Name:               name,
		File:               fmt.Sprintf("%s.yaml", name),
		Version:            scenarioFormatV1,
		Interactions:       make(Interactions, 0),
		sortedInteractions: make(Interactions, 0),
	}

	return s
}

// Load reads a scenario file from disk
func Load(name string) (*Scenario, error) {
	s := NewScenario(name)
	fileName := "record/" + s.File

	data, err := ioutil.ReadFile(fileName)

	if err != nil {
		debugLogf(err.Error())
		return nil, err
	}

	err = yaml.Unmarshal(data, &s)
	for index := range s.Interactions {
		s.Interactions[index].Index = index
	}
	s.sortedInteractions = make(Interactions, len(s.Interactions))
	copy(s.sortedInteractions, s.Interactions)

	return s, err
}

var calls = 0

func transformer(req *Request, i Interaction, res *Response) {
	if req.BodyParsed != nil {
		updateFieldMap(req, &i)
	}
	if res.BodyParsed != nil && len(fields) > 0 {
		updateResFromFieldMap(res)
	}
	saveOrLog(req, fmt.Sprintf("/tmp/%d-request.yaml", calls))
	saveOrLog(i, fmt.Sprintf("/tmp/%d-interaction.yaml", calls))
	saveOrLog(res, fmt.Sprintf("/tmp/%d-response.yaml", calls))
	saveOrLog(fields, fmt.Sprintf("/tmp/%d-fields-map.yaml", calls))
	calls++
}

// AddInteraction appends a new interaction to the scenario
func (s *Scenario) AddInteraction(i *Interaction) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	i.Index = len(s.Interactions)
	s.Interactions = append(s.Interactions, *i)
	s.sortedInteractions = append(s.sortedInteractions, *i)
}

func (s *Scenario) GetInteractionWithFullPath(r Request) (*Interaction, error) {
	newRequest, err := ConverRequestWithFullPath(r)
	if err != nil {
		return nil, err
	}
	return s.GetInteraction(newRequest)
}

// GetInteraction retrieves a recorded request/response interaction
func (s *Scenario) GetInteraction(r Request) (*Interaction, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	sort.Stable(byUsage(s.sortedInteractions))
	if r.Body != "" {
		return s.GetInteractionWithBody(r)
	}
	if strings.Contains(r.URL, "?") {
		return s.GetInteractionWithQueryString(r)
	}
	for _, i := range s.sortedInteractions {
		if s.Matcher(i.Index, &r, &i.Request) {
			s.updateUsageCount(i.Index)
			return &i, nil
		}
	}

	return nil, interactionNotFound
}

// Get match Interaction by compare the query string in request
func (s *Scenario) GetInteractionWithQueryString(r Request) (*Interaction, error) {
	var interactionList []*Interaction
	for index := range s.sortedInteractions {
		if s.Matcher(index, &r, &s.Interactions[index].Request) {
			interactionList = append(interactionList, &s.Interactions[index])
		}
	}
	result, err := s.GetInteractionWithQueryStringFromList(r, interactionList)
	if result != nil {
		s.updateUsageCount(result.Index)
	}
	return result, err
}

func (s *Scenario) GetInteractionWithQueryStringFromList(r Request, list []*Interaction) (*Interaction, error) {
	if len(list) < 1 {
		return nil, interactionNotFound
	}
	if len(list) == 1 {
		return list[0], nil
	}

	// Return a map[string]string[] of query string
	getQueryMap := func(URL string) (url.Values, error) {
		requestURL, err := url.Parse(URL)
		if err != nil {
			return nil, err
		}

		return url.ParseQuery(requestURL.RawQuery)
	}

	// Compare 2 query string and return the credit as each match
	getCreditCompareQueryString := func(m1, m2 url.Values) int {
		var credit int
		for m1Key, m1List := range m1 {
			if m2List, ok := m2[m1Key]; ok {
				if m1List[0] == m2List[0] {
					credit++
				}
			}
		}
		return credit
	}

	var maxCredit int
	var iMax *Interaction

	rQuery, err := getQueryMap(r.URL)
	if err != nil {
		return nil, err
	}

	for _, i := range list {
		credit := 1 - i.Uses
		iQuery, err := getQueryMap(i.URL)
		if err != nil {
			return nil, err
		}

		credit += getCreditCompareQueryString(rQuery, iQuery)
		if credit > maxCredit {
			maxCredit = credit
			iMax = i
		}
	}
	return iMax, nil
}

func (s *Scenario) updateUsageCount(n int) {
	s.Interactions[n].Uses++
	copy(s.sortedInteractions, s.Interactions)
}

// Get Interaction with body by compare the body of request and Interaction
func (s *Scenario) GetInteractionWithBody(r Request) (*Interaction, error) {
	var interactionList []*Interaction
	for index := range s.sortedInteractions {
		if s.Matcher(index, &r, &s.Interactions[index].Request) {
			interactionList = append(interactionList, &s.Interactions[index])
		}
	}
	result, err := s.GetInteractionWithBodyFromList(r, interactionList)
	if result != nil {
		s.updateUsageCount(result.Index)
	}
	return result, err
}

func (s *Scenario) GetInteractionWithBodyFromList(r Request, list []*Interaction) (*Interaction, error) {
	if len(list) < 1 {
		return nil, interactionNotFound
	}
	if len(list) == 1 {
		return list[0], nil
	}

	if r.BodyParsed == nil {
		debugLogf("BodyParsed nil")
		var iMax *Interaction
		var maxCredit int

		for _, i := range list {
			credit := 1 - i.Uses
			if credit > maxCredit {
				maxCredit = credit
				iMax = i
			}
		}
		return iMax, nil
	}

	rBody := r.BodyParsed.(jsonObj)
	var maxCredit int
	var iMax *Interaction
	var credit int
	matchObj := func(iBody jsonObj) {
		for key, rUnk := range rBody {
			if rStringVal, ok := rUnk.(string); ok {
				if iUnk, ok := iBody[key]; ok {
					if iStringVal, ok := iUnk.(string); ok {
						if rStringVal == iStringVal {
							credit++
						}
					}
				}
			}
		}
	}

	debugLogf("In GetInteractionWithBodyFromList with %v items...", len(list))
	for _, i := range list {
		credit = 1 - i.Uses
		if nil == i.Request.BodyParsed {
			i.Request.BodyParsed, _ = unmarshal([]byte(i.Request.Body))
		}
		if iBody, ok := i.Request.BodyParsed.(jsonObj); ok {
			matchObj(iBody)
		} else {
			if aBody, ok := i.Request.BodyParsed.(jsonArr); ok {
				for _, i := range aBody {
					matchObj(i)
				}
			}
		}
		debugLogf("\t...Interaction %v has match %v", i.Index, credit)

		if credit > maxCredit {
			maxCredit = credit
			iMax = i
		}
	}
	debugLogf("\t-> Returning match with number %v", iMax.Index)
	return iMax, nil
}

// Reset returns us to the beginning of the scenario
func (s *Scenario) Reset() {
	for index := range s.Interactions {
		s.Interactions[index].Uses = 0
		s.sortedInteractions[index].Uses = 0
	}
	sort.Stable(byIndex(s.Interactions))
	sort.Stable(byIndex(s.sortedInteractions))
}

// Save writes the scenario data on disk for future re-use
func (s *Scenario) Save() error {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	fileName := "record/" + s.File
	// Create directory for scenario if missing
	scenarioDir := filepath.Dir(fileName)
	if _, err := os.Stat(scenarioDir); os.IsNotExist(err) {
		if err = os.MkdirAll(scenarioDir, 0755); err != nil {
			return err
		}
	}

	// Marshal to YAML and save interactions
	s.Reset()
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}

	f, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
	}()

	_, err = f.Write([]byte("---\n"))
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err != nil {
		return err
	}

	_, err = f.Seek(0, 0)
	if err != nil {
		return err
	}

	//Use to upload the record to cloud
	//err = UploadObject(s.Name, f)
	//if err != nil {
	//	return err
	//}
	return nil
}

var fields = make(map[string]string)

func ConverRequestWithFullPath(r Request) (Request, error) {
	for key, value := range fields {
		if strings.Contains(r.URL, value) {
			r.URL = strings.Replace(r.URL, value, key, -1)
			return r, nil
		}
	}
	return r, interactionNotFound
}
func updateFieldMap(req *Request, i *Interaction) {
	if body, ok := req.BodyParsed.(jsonObj); ok {
		if iBody, ok := i.Request.BodyParsed.(jsonObj); ok {
			for key, unkVal := range body {
				if oldVal, ok := iBody[key].(string); ok {
					if val, ok := unkVal.(string); ok {
						if val != oldVal { // .(string) {
							fields[oldVal] = val
						}
					}
				}
			}
		}
	}
}
func handleUpdateEachJsonObj(body jsonObj) {
	eachValHandle := func(val string, key string) {
		if updatedVal, ok := fields[val]; ok {
			if _, ok := body[key].(string); ok {
				body[key] = updatedVal
			}
		} else {
			for oldVal, changedVal := range fields {
				if len(val) >= len(changedVal) {
					if strings.Contains(val, oldVal) {
						if _, ok := body[key].(string); ok {
							body[key] = strings.Replace(val, oldVal, changedVal, -1)
						} else {
							if asArr, ok := body[key].([]interface{}); ok {
								for index := range asArr {
									if asStr, ok := asArr[index].(string); ok {
										newString := strings.Replace(asStr, oldVal, changedVal, -1)
										asArr[index] = newString
									}

								}
							}

						}

					}
				}
			}
		}
	}
	for key, unkVal := range body {
		if val, ok := unkVal.(string); ok {
			eachValHandle(val, key)
		} else {
			if arrVal, ok := unkVal.([]interface{}); ok {
				for _, val := range arrVal {
					eachValHandle(val.(string), key)
				}
			}
		}
	}
}
func updateResFromFieldMap(res *Response) {
	if body, ok := res.BodyParsed.(jsonObj); ok {
		handleUpdateEachJsonObj(body)
	}
	if iBodyArr, ok := res.BodyParsed.(jsonArr); ok {
		for objIndex, _ := range iBodyArr {
			handleUpdateEachJsonObj(iBodyArr[objIndex])
		}
	}
}
