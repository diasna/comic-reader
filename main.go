package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"errors"
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
	"time"

	"github.com/chai2010/webp"
	"github.com/gorilla/mux"
	"github.com/gosimple/slug"
	_ "github.com/mattn/go-sqlite3"
)

var templates *template.Template
var sqliteDatabase *sql.DB

type Comic struct {
	ID        string
	Title     string
	Artist    string
	Book      string
	Timestamp int64
	Library   bool
}
type ComicLocalPath struct {
	ID        string
	LocalPath string
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	pageParam := r.FormValue("page")
	var page int
	if len(pageParam) < 1 {
		page = 0
	} else {
		var err error
		page, err = strconv.Atoi(pageParam)
		if err != nil {
			log.Println(err)
		}
	}
	limitParam := r.FormValue("limit")
	var limit int
	if len(limitParam) < 1 {
		limit = 12
	} else {
		var err error
		limit, err = strconv.Atoi(limitParam)
		if err != nil {
			log.Println(err)
		}
	}
	libraryParam := r.FormValue("library")
	keywordsParam := r.FormValue("keywords")

	sortByParam := r.FormValue("sort-by")
	var sortBy string
	if len(sortByParam) < 1 {
		sortBy = "import_timestamp"
	} else {
		sortBy = sortByParam
	}

	sortTypeParam := r.FormValue("sort-type")
	var sortType string
	if len(sortByParam) < 1 {
		sortType = "DESC"
	} else {
		sortType = sortTypeParam
	}

	data := struct {
		Previous     int
		Next         int
		Page         int
		Library      string
		Keywords     string
		SortBy       string
		SortType     string
		NumberOfPage int
		Data         []Comic
	}{
		Previous: page - 1,
		Next:     page + 1,
		Page:     page,
		Library:  libraryParam,
		Keywords: keywordsParam,
		SortBy:   sortBy,
		SortType: sortType,
		Data:     searchInDb(page, limit, libraryParam, keywordsParam, sortBy+" "+sortType),
	}
	templates.ExecuteTemplate(w, "index.html", data)
}
func libraryHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	if r.Method == "POST" {
		updateLibrary(id, true)
	} else if r.Method == "DELETE" {
		updateLibrary(id, false)
	}

}
func updateLibrary(id string, library bool) {
	var update string
	if library {
		update = "INSERT OR IGNORE INTO library(comic_id) VALUES (?)"
	} else {
		update = "DELETE FROM library WHERE comic_id = ?"
	}
	_, err := sqliteDatabase.Exec(update, id)
	if err != nil {
		log.Println(err)
	}
}
func searchInDb(page int, limit int, library string, keywords string, sort string) []Comic {
	offset := page * limit
	valid := regexp.MustCompile("^[A-Za-z0-9_ ]+$")
	if !valid.MatchString(sort) {
		log.Println("Invalid input")
		return []Comic{}
	}
	row, err := sqliteDatabase.Query("SELECT c.id, c.title, c.artist, c.book, CAST(c.timestamp AS INTEGER), IIF(l.id, true, false) as library FROM comic c left join library l on c.id = l.comic_id WHERE (ifnull(?, '') = '' OR library = true) AND ((ifnull(?, '') = '' OR c.artist LIKE ?) OR (ifnull(?, '') = '' OR c.title LIKE ?) OR (ifnull(?, '') = '' OR c.book LIKE ?)) ORDER BY "+sort+" LIMIT ?, ?", library, keywords, "%"+keywords+"%", keywords, "%"+keywords+"%", keywords, "%"+keywords+"%", offset, limit)
	var comics []Comic
	if err != nil {
		log.Println(err)
	}
	defer row.Close()
	for row.Next() {
		comic := Comic{}
		row.Scan(&comic.ID, &comic.Title, &comic.Artist, &comic.Book, &comic.Timestamp, &comic.Library)
		comics = append(comics, comic)
	}
	return comics
}
func getLocalPath(id string) ComicLocalPath {
	row, err := sqliteDatabase.Query("SELECT id, local_path FROM comic WHERE id=?", id)
	var comic ComicLocalPath
	if err != nil {
		log.Println(err)
	}
	defer row.Close()
	for row.Next() {
		row.Scan(&comic.ID, &comic.LocalPath)
	}
	return comic
}

func readerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	path := getLocalPath(id)
	zip, errz := zip.OpenReader(path.LocalPath)
	if errz != nil {
		log.Println(errz)
	}
	defer zip.Close()
	var fileList []string
	for _, f := range zip.File {
		fileList = append(fileList, f.Name)
	}

	data := struct {
		ID       string
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
	id, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	w.Header().Set("Content-Type", "image/webp")

	io.Copy(w, bytes.NewBuffer(getCoverFromDb(id)))
}

func pageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, ok := vars["id"]
	if !ok {
		log.Println("id is missing in parameters")
	}
	page, ok := vars["page"]
	if !ok {
		log.Println("page is missing in parameters")
	}

	w.Header().Set("Content-Type", "image/png")
	localPath := getLocalPath(id)
	zip, errx := zip.OpenReader(localPath.LocalPath)
	if errx != nil {
		log.Println(errx)
	}
	defer zip.Close()
	for _, f := range zip.File {
		if f.Name != page {
			continue
		}
		fc, err := f.Open()
		if err != nil {
			log.Println(err)
		}
		defer fc.Close()
		content, err := ioutil.ReadAll(fc)
		if err != nil {
			log.Println(err)
		}
		io.Copy(w, bytes.NewBuffer(content))
	}

}

func getCoverFromDb(id string) []byte {
	row, err := sqliteDatabase.Query("SELECT cover FROM comic WHERE id=?", id)
	if err != nil {
		log.Println(err)
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
				log.Printf("Processing %s", f.Name())
				var buf bytes.Buffer
				var image, err = extractCover(path)
				var title string
				var artist string
				var book string
				if len(reTitle.FindStringSubmatch(f.Name())) < 2 {
					title = f.Name()
				} else {
					title = reTitle.FindStringSubmatch(f.Name())[1]
				}
				if len(reArtist.FindStringSubmatch(f.Name())) < 2 {
					artist = "-"
				} else {
					artist = reArtist.FindStringSubmatch(f.Name())[1]
				}
				if len(reBook.FindStringSubmatch(f.Name())) < 2 {
					book = "-"
				} else {
					book = reBook.FindStringSubmatch(f.Name())[1]
				}
				if err == nil && image != nil {
					data := Comic{
						Title:     title,
						Artist:    artist,
						Book:      book,
						Timestamp: image.ModTime.UnixMilli(),
					}
					if err = webp.Encode(&buf, image.Image, &webp.Options{Lossless: false}); err != nil {
						log.Println(err)
					}
					insertComic(data, path, buf.Bytes())
				} else {
					log.Printf("Skipping %s: %s", path, err.Error())
				}
			}
		}
		return nil
	})
	log.Printf("Done importing %s", path)
}

type Cover struct {
	Image   image.Image
	ModTime time.Time
}

func extractCover(localPath string) (*Cover, error) {
	r, err := zip.OpenReader(localPath)
	if err != nil {
		return nil, errors.New("error opening zip file " + localPath)
	}
	defer r.Close()
	for i, f := range r.File {
		if i == 0 {
			fc, err := f.Open()
			if err != nil {
				return nil, errors.New("error opening image file " + localPath)
			}
			defer fc.Close()
			content, err := ioutil.ReadAll(fc)
			if err != nil {
				return nil, errors.New("error reading image bytes " + localPath)
			}

			img, err := png.Decode(bytes.NewReader(content))
			if err != nil {
				return nil, errors.New("error decoding image " + localPath)
			}
			imagewrapper := &Cover{
				Image:   img,
				ModTime: f.Modified,
			}
			return imagewrapper, nil
		}
	}
	return nil, errors.New("file not found")
}

func initDb() {
	sqliteDatabase, err := sql.Open("sqlite3", "./comic.db")
	if err != nil {
		log.Println(err.Error())
	}
	createComicTableSQL := `CREATE TABLE comic (
		"id" TEXT NOT NULL PRIMARY KEY,		
		"title" TEXT,
		"artist" TEXT,
		"book" TEXT,
		"timestamp" TIMESTAMP,
		"import_timestamp" TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		"local_path" TEXT,
		"cover" BLOB
	  );`
	createStatement, err := sqliteDatabase.Prepare(createComicTableSQL)
	if err != nil {
		log.Println(err.Error())
	}
	createStatement.Exec()
	log.Println("comic table created")
	createLibraryTableSQL := `CREATE TABLE library (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"comic_id" TEXT NOT NULL 
	  );`
	createStatement, err = sqliteDatabase.Prepare(createLibraryTableSQL)
	if err != nil {
		log.Println(err.Error())
	}
	createStatement.Exec()
	log.Println("library table created")
}

func insertComic(comic Comic, localPath string, cover []byte) {
	insertComicSQL := `INSERT OR IGNORE INTO comic(id, title, artist, book, timestamp, import_timestamp, local_path, cover) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?)`
	statement, err := sqliteDatabase.Prepare(insertComicSQL)
	if err != nil {
		log.Println(err.Error())
	}
	_, err = statement.Exec(slug.Make(comic.Title), comic.Title, comic.Artist, comic.Book, comic.Timestamp, localPath, cover)
	if err != nil {
		log.Println(err.Error())
	}
}

func openDb() {
	var err error
	sqliteDatabase, err = sql.Open("sqlite3", "./comic.db")
	if err != nil {
		log.Println(err.Error())
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

	r.HandleFunc("/library/{id}", libraryHandler).Methods("POST")
	r.HandleFunc("/library/{id}", libraryHandler).Methods("DELETE")

	log.Println("Listing for requests at http://localhost:4646/")
	log.Fatal(http.ListenAndServe(":4646", r))
}
func main() {
	if len(os.Args) < 2 {
		startWebServer()
	} else {
		if os.Args[1] == "init" {
			initDb()
		} else if os.Args[1] == "import" {
			path := os.Args[2]
			log.Printf("Importing %s", path)
			reloadComicDb(path)
		}
	}
}
