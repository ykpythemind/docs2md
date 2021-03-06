package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
)

func getClient(config *oauth2.Config) *http.Client {
	tokFile := ".token"
	token, err := getTokenFromFile(tokFile)
	if err != nil {
		token = getTokenFromWeb(config)
		saveToken(tokFile, token)
	}

	return config.Client(context.Background(), token)
}

func getTokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	defer f.Close()
	if err != nil {
		log.Fatalf("Unable to cache OAuth token: %v", err)
	}
	json.NewEncoder(f).Encode(token)
}

func main() {
	ctx := context.Background()
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/documents.readonly")
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	srv, err := docs.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Docs client: %v", err)
	}

	// todo ????????????????????????
	docId := "1KqZd2pXXTppIx6GaIAmdS80ax-eZX1Sp3bptmg3HMYg"
	doc, err := srv.Documents.Get(docId).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve data from document: %v", err)
	}
	fmt.Printf("The title of the doc is: %s\n", doc.Title)
	fmt.Printf("%+v\n", doc.InlineObjects)

	myDoc := &Document{}

	if err = myDoc.Parse(doc); err != nil {
		log.Fatalf("failed to parse: %s", err)
	}

	fmt.Printf("%+v\n", myDoc)
	fmt.Println("---------")

	if err := myDoc.WriteFiles("tmp"); err != nil {
		log.Fatalf("failed to output: %s", err)
	}
}

type Element interface {
	Markdown() string
}

type DocumentImage struct {
	ContentURI  string
	Description string
	ObjectID    string
}

type Header1Element struct {
	Body string
}

func (e Header1Element) Markdown() string {
	return fmt.Sprintf("# %s\n", e.Body)
}

type TextElement struct {
	Body string
}

type ImageElement struct {
	Image *DocumentImage
}

func (e ImageElement) Markdown() string {
	// temp
	// ????????????????????????
	return fmt.Sprintf("![%s](%s.jpg)\n", e.Image.Description, e.Image.ObjectID)
}

func (e ImageElement) String() string {
	return e.Markdown()
}

func (e TextElement) Markdown() string {
	return fmt.Sprintf("%s\n", e.Body)
}

// TODO: PageBreak

type Document struct {
	Title            string
	Elements         []Element
	Images           map[string]DocumentImage
	originalDocument *docs.Document
}

func (d *Document) Parse(doc *docs.Document) error {
	d.originalDocument = doc

	d.Title = doc.Title

	for _, b := range doc.Body.Content {
		err := d.parseBody(b)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Document) parseBody(elm *docs.StructuralElement) error {
	if elm == nil {
		return nil
	}

	// only parse Paragraph
	paragraph := elm.Paragraph
	if paragraph == nil {
		return nil
	}

	for _, e := range paragraph.Elements {
		if e.TextRun != nil {
			content := e.TextRun.Content
			if paragraph.ParagraphStyle.NamedStyleType == "TITLE" {
				d.add(Header1Element{Body: content})
			} else {
				d.add(TextElement{Body: content})
			}
			continue
		}

		if e.InlineObjectElement != nil {
			d.handleInlineObjectElement(e.InlineObjectElement)
			continue
		}
	}

	return nil
}

func (d *Document) add(elm Element) {
	d.Elements = append(d.Elements, elm)
}

func (d *Document) handleInlineObjectElement(elm *docs.InlineObjectElement) {
	if inlineObject, ok := d.originalDocument.InlineObjects[elm.InlineObjectId]; ok {
		pro := inlineObject.InlineObjectProperties
		if pro == nil {
			return
		}

		if obj := pro.EmbeddedObject; obj != nil {
			if im := obj.ImageProperties; im != nil {
				d.add(&ImageElement{Image: &DocumentImage{
					ObjectID: inlineObject.ObjectId, ContentURI: im.ContentUri, Description: obj.Description,
				}})
			}
		}
	}
}

func (d *Document) WriteFiles(dir string) error {
	i, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !i.IsDir() {
		return fmt.Errorf("%s is not directory", dir)
	}

	var b bytes.Buffer
	for _, elm := range d.Elements {
		b.WriteString(elm.Markdown())
	}

	// fmt.Println(b.String())

	f, err := os.OpenFile(path.Join(dir, fmt.Sprintf("%s.md", d.Title)), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(b.Bytes()); err != nil {
		return err
	}

	// download file
	for _, elm := range d.Elements {
		image, ok := elm.(*ImageElement)
		if !ok {
			continue
		}

		if err := download(*image, dir); err != nil {
			return err
		}
	}

	return nil
}

func download(image ImageElement, dir string) error {
	// TODO: ????????????????????????????????????

	resp, err := http.Get(image.Image.ContentURI)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// fname := strings.Replace(image.Image.ObjectID, ".", "_", -1)
	fname := image.Image.ObjectID
	f, err := os.OpenFile(path.Join(dir, fmt.Sprintf("%s.jpg", fname)), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	return nil
}
