package main

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"time"
)

const PORT = ":8090"
const UrlLimit = 20
const LimitOutgoingConnections = 3
const HttpLimitPerSecons = 100
const HttpLimitPerSeconsBoost = 140

//Request from client
type CheckRequest struct {
	Urls []string
}

//Response to client
type CheckResponse struct {
	Urls []UrlCheckResult `json:"urls"`
}

//Url
type Url struct {
	path string
}

//Url with check result
type UrlCheckResult struct {
	Url *Url `json:"url"`
	Code int `json:"code"`
	Message  string `json:"message"`
	Time     float64
}

/**
	HTTP server limit (f.e.  100 connection per second)
 */
var limiter = rate.NewLimiter(HttpLimitPerSecons, HttpLimitPerSeconsBoost)
func limit(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limiter.Allow() == false {
			http.Error(w, http.StatusText(429), http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/check", checkHandler)

	server := &http.Server{
		Addr: PORT,
		Handler: limit(mux),
		ReadTimeout:  time.Minute,
		WriteTimeout: time.Minute,
	}

	//Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			fmt.Printf("Error: %v\n", err)
			stop <- os.Kill
			return
		}
		fmt.Println("Server started.")
	}()

	<-stop

	if err := server.Shutdown(context.Background()); err != nil {
		fmt.Println("Server error...")
	}

	fmt.Println("Server stopped.")
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	g, ctx := errgroup.WithContext(r.Context())

	//Decode request
	var req CheckRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Urls) > UrlLimit {
		http.Error(w, "{'error' : 'to many urls'}", http.StatusBadRequest)
		return
	}


	resultChan := make(chan UrlCheckResult)
	defer close(resultChan)

	//Wait group for urls checks
	gr := sync.WaitGroup{}
	gr.Add(len(req.Urls))

	//Parallel limit
	limitQueue := make(chan string, LimitOutgoingConnections)
	defer close(limitQueue)

	/**
		Goroutine that handle check result.
	*/
	var CheckResult []UrlCheckResult
	go func(resultChan chan UrlCheckResult) {
		for {
			if checkResult, ok := <-resultChan; ok {
				if checkResult.Code > 0 {
					//fmt.Printf("done: %s %d %s\n", checkResult.Url.path, checkResult.Code, checkResult.Message)
				} else {
					//fmt.Printf("done (ignore): %s\n", checkResult.Url.path)
				}

				CheckResult = append(CheckResult, checkResult)
				<-limitQueue
				gr.Done()
			} else {
				break
			}
		}
	}(resultChan)

	/**
		Workers that checks urls
	*/
	for _, path := range req.Urls {
		limitQueue <- path
		path := path

		g.Go(func() error {
			select {
				case <-ctx.Done():
					resultChan <- UrlCheckResult{Url: &Url{path: path}}
					return fmt.Errorf("cancelled by client")
				default:
			}

			res := CheckUrl(Url{path: path}, resultChan, ctx)
			return res
		})
	}

	if err := g.Wait(); err != nil {
		//fmt.Printf("Urls has error: %v", err)
		fmt.Print(".")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	gr.Wait()
	fmt.Print("+")
	w.Header().Set("Content-Type", "application/json")

	fooMarshalled, err := json.Marshal( CheckResponse{Urls: CheckResult}); if err != nil {
		_, err = fmt.Fprint(w, "{}"); if err != nil {
			fmt.Print("0")
		}
		return
	}

	_, err = fmt.Fprint(w, string(fooMarshalled)); if err != nil {
		fmt.Print("!")
	}
}

func CheckUrl(url Url, ch chan <- UrlCheckResult, ctx context.Context) error {
	time.Sleep(0 * time.Second)

	start := time.Now()
	client := http.Client{
		Timeout: 1 * time.Second,
		//CheckRedirect: func(req *http.Request, via []*http.Request) error {
		//	return http.ErrUseLastResponse
		//},
	}

	resp, err := client.Get(url.path); if err != nil {
		secs := time.Since(start).Seconds()
		ch <- UrlCheckResult{
			Url: &url,
			Code: 10,
			Message: fmt.Sprintf("%.2f Resp error: %s", secs, url.path),
			Time: secs}

		return fmt.Errorf("error (a) in %s", url.path)
	}

	body, err := ioutil.ReadAll(resp.Body); if err != nil {
		secs := time.Since(start).Seconds()
		ch <- UrlCheckResult{
			Url: &url,
			Code:resp.StatusCode,
			Message: fmt.Sprintf("%.2f No body: %s", secs, url.path),
			Time: secs}

		return fmt.Errorf("error (b) in %s", url.path)
	}

	defer resp.Body.Close()

	secs := time.Since(start).Seconds()

	ch <- UrlCheckResult{
		Url: &url,
		Code: resp.StatusCode,
		Message: fmt.Sprintf("%.2f Resp length: %dkb %s code: %d", secs, len(body)/1024, url.path, resp.StatusCode),
		Time: secs}

	return nil
}
