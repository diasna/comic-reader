package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"html/template"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/chai2010/webp"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
)

var templates *template.Template
var sqliteDatabase *sql.DB

type Comic struct {
	ID        int
	Title     string
	Artist    string
	Book      string
	Timestamp int64
}
type ComicLocalPath struct {
	ID        int
	LocalPath string
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	pageParam := r.FormValue("page")
	var page int
	if len(pageParam) < 1 {
		page = 1
	} else {
		var err error
		page, err = strconv.Atoi(pageParam)
		if err != nil {
			log.Fatal(err)
		}
	}
	data := struct {
		Previous     int
		Next         int
		Page         int
		NumberOfPage int
		Data         []Comic
	}{
		Previous: page - 1,
		Next:     page + 1,
		Page:     page,
		Data:     searchInDb(page),
	}
	templates.ExecuteTemplate(w, "index.html", data)
}

func searchInDb(page int) []Comic {
	limit := 12
	offset := page * limit
	row, err := sqliteDatabase.Query("SELECT id, title, artist, book, CAST(timestamp AS INTEGER) FROM comic ORDER BY timestamp DESC LIMIT ?, ?", offset, limit)
	var comics []Comic
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()
	for row.Next() {
		comic := Comic{}
		row.Scan(&comic.ID, &comic.Title, &comic.Artist, &comic.Book, &comic.Timestamp)
		comics = append(comics, comic)
	}
	return comics
}
func getLocalPath(id int) ComicLocalPath {
	row, err := sqliteDatabase.Query("SELECT id, local_path FROM comic WHERE id=?", id)
	var comic ComicLocalPath
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()
	for row.Next() {
		row.Scan(&comic.ID, &comic.LocalPath)
	}
	return comic
}

func readerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idstr, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	id, err := strconv.Atoi(idstr)
	if err != nil {
		log.Fatal(err)
	}

	path := getLocalPath(id)
	zip, errz := zip.OpenReader(path.LocalPath)
	if errz != nil {
		log.Fatal(errz)
	}
	defer zip.Close()
	var fileList []string
	for _, f := range zip.File {
		fileList = append(fileList, f.Name)
	}

	data := struct {
		ID       int
		FileList []string
	}{
		ID:       path.ID,
		FileList: fileList,
	}
	if err := templates.ExecuteTemplate(w, "reader.html", data); err != nil {
		log.Printf("Template error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func coverHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idstr, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	intVar, err := strconv.Atoi(idstr)
	if err != nil {
		log.Fatal(err)
	}
	w.Header().Set("Content-Type", "image/webp")

	io.Copy(w, bytes.NewBuffer(getCoverFromDb(intVar)))
}

func pageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idstr, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	id, err := strconv.Atoi(idstr)
	if err != nil {
		log.Fatal(err)
	}

	page, ok := vars["page"]
	if !ok {
		log.Println("page is missing in parameters")
	}

	w.Header().Set("Content-Type", "image/png")
	localPath := getLocalPath(id)
	zip, errx := zip.OpenReader(localPath.LocalPath)
	if errx != nil {
		log.Fatal(errx)
	}
	defer zip.Close()
	for _, f := range zip.File {
		if f.Name != page {
			continue
		}
		fc, err := f.Open()
		if err != nil {
			log.Fatal(err)
		}
		defer fc.Close()
		content, err := ioutil.ReadAll(fc)
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(w, bytes.NewBuffer(content))
	}

}

func getCoverFromDb(id int) []byte {
	row, err := sqliteDatabase.Query("SELECT cover FROM comic WHERE id=?", id)
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()
	var data []byte
	for row.Next() {
		row.Scan(&data)
	}
	return data
}

func reloadComicDb(path string) {
	openDb()
	reTitle := regexp.MustCompile(`(?s)\] (.*) \(`)
	reArtist := regexp.MustCompile(`(?s)\[(.*)\]`)
	reBook := regexp.MustCompile(`(?s)\((.*)\)`)
	filepath.Walk(path, func(path string, f os.FileInfo, _ error) error {
		if !f.IsDir() {
			r, err := regexp.MatchString(".zip", f.Name())
			if err == nil && r {
				log.Println(path)
				data := Comic{
					Title:     reTitle.FindStringSubmatch(f.Name())[1],
					Artist:    reArtist.FindStringSubmatch(f.Name())[1],
					Book:      reBook.FindStringSubmatch(f.Name())[1],
					Timestamp: f.ModTime().UnixMilli(),
				}
				var buf bytes.Buffer
				var image = extractCover(path)
				if err = webp.Encode(&buf, image, &webp.Options{Lossless: false}); err != nil {
					log.Println(err)
				}
				insertComic(data, path, buf.Bytes())
			}
		}
		return nil
	})
}

func extractCover(localPath string) image.Image {
	r, err := zip.OpenReader(localPath)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()
	for i, f := range r.File {
		if i == 0 {
			fc, err := f.Open()
			if err != nil {
				log.Fatal(err)
			}
			defer fc.Close()
			content, err := ioutil.ReadAll(fc)
			if err != nil {
				log.Fatal(err)
			}

			img, err := png.Decode(bytes.NewReader(content))
			if err != nil {
				log.Fatalln(err)
			}
			return img
		}
	}
	return nil
}

func resetDb() {
	os.Remove("comic.db")
	log.Println("Creating comic.db...")
	file, err := os.Create("comic.db")
	if err != nil {
		log.Fatal(err.Error())
	}
	file.Close()
	log.Println("comic.db created")
	sqliteDatabase, err = sql.Open("sqlite3", "./comic.db")
	if err != nil {
		log.Fatal(err.Error())
	}
	createComicTableSQL := `CREATE TABLE comic (
		"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,		
		"title" TEXT,
		"artist" TEXT,
		"book" TEXT,
		"timestamp" TIMESTAMP,
		"local_path" TEXT,
		"cover" BLOB 
	  );`

	log.Println("Create comic table...")
	statement, err := sqliteDatabase.Prepare(createComicTableSQL)
	if err != nil {
		log.Fatal(err.Error())
	}
	statement.Exec()
	log.Println("comic table created")
}

func insertComic(comic Comic, localPath string, cover []byte) {
	log.Printf("Inserting %s", comic.Title)
	insertComicSQL := `INSERT INTO comic(title, artist, book, timestamp, local_path, cover) VALUES (?, ?, ?, ?, ?, ?)`
	statement, err := sqliteDatabase.Prepare(insertComicSQL)
	if err != nil {
		log.Fatalln(err.Error())
	}
	_, err = statement.Exec(comic.Title, comic.Artist, comic.Book, comic.Timestamp, localPath, cover)
	if err != nil {
		log.Fatalln(err.Error())
	}
}

func openDb() {
	var err error
	sqliteDatabase, err = sql.Open("sqlite3", "./comic.db")
	if err != nil {
		log.Fatal(err.Error())
	}
}

func startWebServer() {
	openDb()
	templates = template.Must(templates.ParseGlob("templates/*.html"))
	r := mux.NewRouter()
	r.HandleFunc("/", indexHandler)
	r.HandleFunc("/reader/{id}", readerHandler)
	r.HandleFunc("/reader/{id}/{page}", pageHandler)
	r.HandleFunc("/covers/{id}", coverHandler)
	log.Println("Listing for requests at http://localhost:4646/")
	log.Fatal(http.ListenAndServe(":4646", r))
}
func main() {
	if len(os.Args) < 2 {
		startWebServer()
	} else {
		if os.Args[1] == "reset" {
			resetDb()
		} else if os.Args[1] == "import" {
			path := os.Args[2]
			log.Printf("Importing %s", path)
			reloadComicDb(path)
		}
	}
}
