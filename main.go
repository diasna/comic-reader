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
	libraryParam := r.FormValue("library")
	keywordsParam := r.FormValue("keywords")
	data := struct {
		Previous     int
		Next         int
		Page         int
		Library      string
		Keywords     string
		NumberOfPage int
		Data         []Comic
	}{
		Previous: page - 1,
		Next:     page + 1,
		Page:     page,
		Library:  libraryParam,
		Keywords: keywordsParam,
		Data:     searchInDb(page, libraryParam, keywordsParam),
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
	_, err := sqliteDatabase.Exec("UPDATE comic SET library =? WHERE id =?", library, id)
	if err != nil {
		log.Println(err)
	}
}
func searchInDb(page int, library string, keywords string) []Comic {
	limit := 12
	offset := page * limit
	row, err := sqliteDatabase.Query("SELECT id, title, artist, book, CAST(timestamp AS INTEGER), library FROM comic WHERE (ifnull(?, '') = '' OR library = 1) AND ((ifnull(?, '') = '' OR artist LIKE ?) OR (ifnull(?, '') = '' OR title LIKE ?) OR (ifnull(?, '') = '' OR book LIKE ?)) ORDER BY timestamp DESC LIMIT ?, ?", library, keywords, "%"+keywords+"%", keywords, "%"+keywords+"%", keywords, "%"+keywords+"%", offset, limit)
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
				var buf bytes.Buffer
				var image, err = extractCover(path)

				if err == nil && image != nil {
					data := Comic{
						Title:     reTitle.FindStringSubmatch(f.Name())[1],
						Artist:    reArtist.FindStringSubmatch(f.Name())[1],
						Book:      reBook.FindStringSubmatch(f.Name())[1],
						Timestamp: image.ModTime.UnixMilli(),
					}
					if err = webp.Encode(&buf, image.Image, &webp.Options{Lossless: false}); err != nil {
						log.Println(err)
					}
					insertComic(data, path, buf.Bytes())
					log.Printf("Inserting %s", data.Title)
				} else {
					log.Printf("Skipping %s", path)
				}
			}
		}
		return nil
	})
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

func resetDb() {
	sqliteDatabase, err := sql.Open("sqlite3", "./comic.db")
	if err != nil {
		log.Println(err.Error())
	}
	dropComicTableSQL := `DROP TABLE comic;`
	log.Println("dropping comic table...")
	droptStatement, err := sqliteDatabase.Prepare(dropComicTableSQL)
	if err != nil {
		log.Println(err.Error())
	} else {
		droptStatement.Exec()
		log.Println("comic table dropped")
	}
	createComicTableSQL := `CREATE TABLE comic (
		"id" TEXT NOT NULL PRIMARY KEY,		
		"title" TEXT,
		"artist" TEXT,
		"book" TEXT,
		"timestamp" TIMESTAMP,
		"local_path" TEXT,
		"cover" BLOB,
		"library" BOOLEAN 
	  );`

	log.Println("Create comic table...")
	createStatement, err := sqliteDatabase.Prepare(createComicTableSQL)
	if err != nil {
		log.Println(err.Error())
	}
	createStatement.Exec()
	log.Println("comic table created")
}

func insertComic(comic Comic, localPath string, cover []byte) {
	insertComicSQL := `INSERT OR IGNORE INTO comic(id, title, artist, book, timestamp, local_path, cover) VALUES (?, ?, ?, ?, ?, ?, ?)`
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
		if os.Args[1] == "reset" {
			resetDb()
		} else if os.Args[1] == "import" {
			path := os.Args[2]
			log.Printf("Importing %s", path)
			reloadComicDb(path)
		}
	}
}
