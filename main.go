package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
)

// Struktur untuk menangani request body
type ScrapRequest struct {
	Action string `json:"action"`
	URL    string `json:"url"`
}

// Struktur untuk response data dari scraping URL
type ScrapResponse struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Content  string `json:"content"`
	Category string `json:"category"`
}

// Struktur untuk response utama
type MainResponse struct {
	Response     string        `json:"response"`
	ResponseTime string        `json:"responseTime"`
	Message      string        `json:"message"`
	Data         ScrapResponse `json:"data"`
}

// Response struct untuk format JSON yang akan dikembalikan dari GET
type Response struct {
	Response     string   `json:"response"`
	Message      string   `json:"message"`
	ResponseTime string   `json:"responseTime"` // Mengubah field responseTime menjadi string
	Data         DapoData `json:"data"`
}

// DapoData struct untuk menyimpan bagian "dapo" di dalam data
type DapoData struct {
	Dapo BeritaData `json:"dapo"`
}

// BeritaData struct untuk menyimpan bagian "berita" di dalam dapo
type BeritaData struct {
	Berita []NewsArticle `json:"berita"`
}

// NewsArticle struct untuk menyimpan data berita
type NewsArticle struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	Link  string `json:"link"`
	Image string `json:"image"`
	User  string `json:"user"`
	Tags  string `json:"tags"`
}

func main() {
	http.HandleFunc("/dapo/berita", beritaHandler)
	fmt.Println("Server berjalan di port 8080...\nhttp://localhost:8080/dapo/berita")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Fungsi untuk menangani permintaan GET dan POST
func beritaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		scrapHandler(w, r)
	} else if r.Method == "GET" {
		handler(w, r)
	} else {
		http.Error(w, "Hanya GET dan POST yang didukung", http.StatusMethodNotAllowed)
	}
}

// Fungsi untuk menangani permintaan POST
func scrapHandler(w http.ResponseWriter, r *http.Request) {
	// Decode body dari request
	var req ScrapRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Cek apakah action adalah 'read'
	if req.Action != "read" {
		http.Error(w, "Action tidak didukung", http.StatusBadRequest)
		return
	}

	// Mulai waktu untuk mengukur respons
	startTime := time.Now()

	// Lakukan scraping URL
	responseData, err := scrapeURL(req.URL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error scraping URL: %v", err), http.StatusInternalServerError)
		return
	}

	// Menghitung waktu respons
	elapsed := time.Since(startTime)
	responseTime := fmt.Sprintf("%v ms", elapsed.Milliseconds())

	// Membuat respons utama
	mainResponse := MainResponse{
		Response:     "200",
		ResponseTime: responseTime,
		Message:      "Success",
		Data:         *responseData,
	}

	// Set response dalam bentuk JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mainResponse)
}

// Fungsi untuk scraping HTML dari URL
func scrapeURL(url string) (*ScrapResponse, error) {
	// Request ke URL
	doc, err := goquery.NewDocument(url)
	if err != nil {
		return nil, err
	}

	// Ekstraksi data yang diinginkan
	title := doc.Find("h2:nth-child(1)").Text()
	author := strings.TrimSpace(doc.Find(".glyphicon-user").Parent().Text())
	date := strings.TrimSpace(doc.Find(".glyphicon-calendar").Parent().First().Text()) // Ambil hanya tanggal pertama
	category := strings.TrimSpace(doc.Find(".glyphicon-tag").Parent().Text())

	// Membersihkan teks
	author = strings.Replace(author, "Diposkan Oleh :", "", 1)
	date = strings.Replace(date, "Tanggal :", "", 1)
	category = strings.Replace(category, "Kategori :", "", 1)

	// Mengambil semua konten dari tag <p>
	var contentBuilder strings.Builder
	doc.Find("p").Each(func(i int, s *goquery.Selection) {
		contentBuilder.WriteString(s.Text() + " ") // Tambahkan spasi setelah setiap paragraf
	})

	title = strings.ReplaceAll(title, "Berita Lainnya", "")
	title = strings.ReplaceAll(title, "Kalender Pendataan", "")
	title = strings.ReplaceAll(title, "Foto Gallery", "")
	title = strings.TrimSpace(title) // Menghapus spasi di awal dan akhir

	// Mengembalikan hasil scraping
	return &ScrapResponse{
		Title:    strings.TrimSpace(title),
		Author:   strings.TrimSpace(author),
		Date:     strings.TrimSpace(date), // Hanya tanggal pertama yang diambil
		Content:  strings.TrimSpace(contentBuilder.String()),
		Category: strings.TrimSpace(category),
	}, nil
}

// Fungsi untuk scrape data secara konkuren dari beberapa halaman
func scrapeData(baseUrl string) (Response, error) {
	startTime := time.Now() // Mulai waktu penghitungan
	var articles []NewsArticle
	var wg sync.WaitGroup
	articleChannel := make(chan []NewsArticle)
	page := 1
	maxPages := 50                              // Batas maksimal halaman yang di-scrape untuk contoh
	concurrentLimit := 50                       // Limit goroutine yang berjalan bersamaan
	sem := make(chan struct{}, concurrentLimit) // Channel untuk batasi goroutine

	// Goroutine yang mengumpulkan data dari channel
	go func() {
		for pageArticles := range articleChannel {
			articles = append(articles, pageArticles...)
		}
	}()

	// Lakukan scraping untuk setiap halaman
	for page <= maxPages {
		wg.Add(1)
		sem <- struct{}{} // Menambah goroutine ke semaphore

		go func(page int) {
			defer wg.Done()
			defer func() { <-sem }() // Melepaskan goroutine dari semaphore

			url := fmt.Sprintf("%s/laman/%d", baseUrl, page)
			pageArticles, err := scrapePage(url)
			if err == nil && len(pageArticles) > 0 {
				articleChannel <- pageArticles
			}
		}(page)

		page++
	}

	// Tunggu hingga semua goroutine selesai
	wg.Wait()
	close(articleChannel)

	// Mengurutkan artikel berdasarkan tanggal
	sort.Slice(articles, func(i, j int) bool {
		dateI, _ := parseDate(articles[i].Date)
		dateJ, _ := parseDate(articles[j].Date)
		return dateI.After(dateJ) // Urutan terbaru di atas
	})

	if len(articles) == 0 {
		return Response{Response: "404", Message: "No data found"}, nil
	}

	// Menghitung waktu respons dalam milidetik
	responseTime := time.Since(startTime).Milliseconds()

	// Mengubah responseTime menjadi string
	responseTimeString := fmt.Sprintf("%d ms", responseTime)

	return Response{
		Response:     "200",
		ResponseTime: responseTimeString, // Menambahkan waktu respons sebagai string
		Message:      "success",
		Data:         DapoData{Dapo: BeritaData{Berita: articles}},
	}, nil
}

// Fungsi untuk scrape data dari satu halaman
func scrapePage(url string) ([]NewsArticle, error) {
	c := colly.NewCollector()
	var articles []NewsArticle

	// Callback untuk setiap item berita
	c.OnHTML(".card-body.card-padding-lg .media", func(e *colly.HTMLElement) {
		title := e.ChildText("h3.media-heading")
		date := e.ChildText("ul.list-inline li:nth-child(3) span")
		tags := e.ChildText("ul.list-inline li:nth-child(5) span")
		image := e.ChildAttr("img", "src")
		user := e.ChildText("ul.list-inline li:nth-child(1) span")
		link := e.ChildAttr("a.pull-left", "href")
		if link != "" {
			link = "https://dapo.kemdikbud.go.id" + link
		}
		if image != "" {
			image = "https://dapo.kemdikbud.go.id" + image
		}

		articles = append(articles, NewsArticle{
			Title: title,
			Date:  date,
			Link:  link,
			Image: image,
			User:  user,
			Tags:  tags,
		})
	})

	// Mulai scraping
	err := c.Visit(url)
	if err != nil {
		return nil, err
	}

	return articles, nil
}

// Fungsi untuk parsing tanggal
func parseDate(dateStr string) (time.Time, error) {
	// Menghapus karakter tambahan dan mengubah format ke RFC3339
	dateStr = strings.TrimSpace(dateStr)
	dateStr = strings.ReplaceAll(dateStr, "Januari", "January")
	dateStr = strings.ReplaceAll(dateStr, "Februari", "February")
	dateStr = strings.ReplaceAll(dateStr, "Maret", "March")
	dateStr = strings.ReplaceAll(dateStr, "April", "April")
	dateStr = strings.ReplaceAll(dateStr, "Mei", "May")
	dateStr = strings.ReplaceAll(dateStr, "Juni", "June")
	dateStr = strings.ReplaceAll(dateStr, "Juli", "July")
	dateStr = strings.ReplaceAll(dateStr, "Agustus", "August")
	dateStr = strings.ReplaceAll(dateStr, "September", "September")
	dateStr = strings.ReplaceAll(dateStr, "Oktober", "October")
	dateStr = strings.ReplaceAll(dateStr, "November", "November")
	dateStr = strings.ReplaceAll(dateStr, "Desember", "December")
	dateStr = strings.ReplaceAll(dateStr, "|", "")
	dateStr = strings.ReplaceAll(dateStr, "&nbsp;", "")

	// Membuat regex untuk mengekstrak tanggal
	re := regexp.MustCompile(`(\d{1,2})\s([A-Za-z]+)\s(\d{4})`)
	matches := re.FindStringSubmatch(dateStr)
	if len(matches) != 4 {
		return time.Time{}, fmt.Errorf("invalid date format")
	}

	dateFormatted := fmt.Sprintf("%s %s %s", matches[1], matches[2], matches[3])
	parsedDate, err := time.Parse("02 January 2006", dateFormatted)
	return parsedDate, err
}

// Handler untuk API endpoint GET
func handler(w http.ResponseWriter, r *http.Request) {
	url := "https://dapo.kemdikbud.go.id/berita"
	response, err := scrapeData(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
