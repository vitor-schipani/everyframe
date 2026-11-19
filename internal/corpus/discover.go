package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAPIURL is the Wikimedia Commons MediaWiki API endpoint.
	DefaultAPIURL = "https://commons.wikimedia.org/w/api.php"
	// DefaultCategory is the Commons category candidates are drawn from.
	DefaultCategory = "Category:Animated GIF files"

	DefaultCount         = 100
	DefaultMaxFileBytes  = 20 << 20
	DefaultMaxTotalBytes = 500 << 20

	ExtendedCount         = 1000
	ExtendedMaxTotalBytes = 3 << 30

	smallMaxBytes  = 256 << 10
	mediumMaxBytes = 4 << 20
)

// ExtendedCategories are the Commons categories the extended tier is drawn
// from. Besides the general category they cover what it has little of: files
// with very many pixels, photographs, flags and maps, transparency, the output
// of particular encoders, single-frame GIFs and files known to be malformed.
var ExtendedCategories = []string{
	DefaultCategory,
	"Category:Animated GIF files between 50 MP and 100 MP",
	"Category:Animated GIF files exceeding the 100 MP limit",
	"Category:Animated GIF maps",
	"Category:Animated GIF flags",
	"Category:Animated GIF throbbers",
	"Category:Animated GIF files of people",
	"Category:Animated GIF files with transparent background",
	"Category:Animated GIF files with black background",
	"Category:Animated Earth",
	"Category:Animated GIF skies",
	"Category:Time-lapse animations",
	"Category:GIF animations created with Blender",
	"Category:GIF animations assembled with gifsicle",
	"Category:Still GIF files",
	"Category:GIF files with errors",
	"Category:True color GIF files",
}

// DiscoverOptions controls how a manifest is built from the Commons API.
// Zero values select the defaults.
type DiscoverOptions struct {
	APIURL string
	// Categories are the Commons categories sampled. Defaults to
	// DefaultCategory alone.
	Categories    []string
	Count         int
	MaxFileBytes  int64
	MaxTotalBytes int64
	// Still also accepts GIFs that are not animated.
	Still bool
	// Exclude holds the checksums of files that must not be selected, such
	// as those another tier already has.
	Exclude map[string]bool
	// Prefixes are the category sort-key prefixes sampled, one listing each,
	// so candidates are spread across the alphabet instead of all starting
	// with the same characters. Defaults to A-Z and 0-9.
	Prefixes []string
	// PagesPerPrefix is how many 50-file listing pages to read per prefix.
	PagesPerPrefix int
	// Delay is the pause between API requests.
	Delay     time.Duration
	UserAgent string
	Client    *http.Client
	Retries   int
	Backoff   time.Duration
	Now       func() time.Time
	// Progress, if set, is called after each prefix of each category has been
	// listed.
	Progress func(DiscoverEvent)
}

// DiscoverEvent reports how far the category sampling has got.
type DiscoverEvent struct {
	// Category and Prefix are the category and the sort-key prefix that were
	// just listed.
	Category, Prefix string
	// Done and Total count listings: one per category and prefix.
	Done, Total int
	// Candidates is the number of acceptable files found so far.
	Candidates int
}

func (o DiscoverOptions) withDefaults() DiscoverOptions {
	if o.APIURL == "" {
		o.APIURL = DefaultAPIURL
	}
	if len(o.Categories) == 0 {
		o.Categories = []string{DefaultCategory}
	}
	categories := make([]string, len(o.Categories))
	for i, c := range o.Categories {
		if !strings.HasPrefix(c, "Category:") {
			c = "Category:" + c
		}
		categories[i] = c
	}
	o.Categories = categories
	if o.Count <= 0 {
		o.Count = DefaultCount
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if len(o.Prefixes) == 0 {
		o.Prefixes = strings.Split("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "")
	}
	if o.PagesPerPrefix <= 0 {
		o.PagesPerPrefix = 1
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	o.Client = defaultClient(o.Client)
	if o.Retries == 0 {
		o.Retries = defaultRetries
	}
	if o.Backoff == 0 {
		o.Backoff = defaultBackoff
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

type apiResponse struct {
	BatchComplete bool              `json:"batchcomplete"`
	Continue      map[string]string `json:"continue"`
	Error         *struct {
		Code string `json:"code"`
		Info string `json:"info"`
	} `json:"error"`
	Query struct {
		Pages []apiPage `json:"pages"`
	} `json:"query"`
}

type apiPage struct {
	PageID    int64  `json:"pageid"`
	Title     string `json:"title"`
	ImageInfo []struct {
		Size           int64   `json:"size"`
		Width          int     `json:"width"`
		Height         int     `json:"height"`
		Duration       float64 `json:"duration"`
		URL            string  `json:"url"`
		DescriptionURL string  `json:"descriptionurl"`
		SHA1           string  `json:"sha1"`
		Mime           string  `json:"mime"`
		ExtMetadata    map[string]struct {
			Value any `json:"value"`
		} `json:"extmetadata"`
	} `json:"imageinfo"`
}

// Discover samples the categories and returns a manifest of selected files.
// The selection is deterministic for a given set of API results.
func Discover(ctx context.Context, opts DiscoverOptions) (*Manifest, error) {
	opts = opts.withDefaults()
	seen := make(map[string]Entry)
	done, total := 0, len(opts.Categories)*len(opts.Prefixes)
	for _, category := range opts.Categories {
		for _, prefix := range opts.Prefixes {
			if done > 0 {
				if err := sleep(ctx, opts.Delay); err != nil {
					return nil, err
				}
			}
			if err := collectPrefix(ctx, opts, category, prefix, seen); err != nil {
				return nil, fmt.Errorf("list %q from prefix %q: %w", category, prefix, err)
			}
			done++
			if opts.Progress != nil {
				opts.Progress(DiscoverEvent{Category: category, Prefix: prefix, Done: done, Total: total, Candidates: len(seen)})
			}
		}
	}
	candidates := make([]Entry, 0, len(seen))
	for _, e := range seen {
		candidates = append(candidates, e)
	}
	return &Manifest{
		Source:      opts.APIURL,
		Categories:  opts.Categories,
		GeneratedAt: opts.Now().UTC().Format(time.RFC3339),
		Selection: Selection{
			Count:         opts.Count,
			MaxFileBytes:  opts.MaxFileBytes,
			MaxTotalBytes: opts.MaxTotalBytes,
			Still:         opts.Still,
		},
		Entries: selectEntries(candidates, opts.Count, opts.MaxTotalBytes),
	}, nil
}

// collectPrefix reads up to PagesPerPrefix listing pages of category starting
// at prefix and adds every acceptable file to seen, keyed by checksum.
func collectPrefix(ctx context.Context, opts DiscoverOptions, category, prefix string, seen map[string]Entry) error {
	params := url.Values{
		"action":                {"query"},
		"format":                {"json"},
		"formatversion":         {"2"},
		"generator":             {"categorymembers"},
		"gcmtitle":              {category},
		"gcmtype":               {"file"},
		"gcmlimit":              {"50"},
		"gcmstartsortkeyprefix": {prefix},
		"prop":                  {"imageinfo"},
		"iiprop":                {"url|size|sha1|mime|extmetadata"},
		"iiextmetadatafilter":   {"LicenseShortName|Artist"},
	}
	// A listing page can take several requests when the API splits the
	// imageinfo for it; batchcomplete marks the end of each page.
	const maxRequestsPerPage = 20
	pages := 0
	for range opts.PagesPerPrefix * maxRequestsPerPage {
		resp, err := apiQuery(ctx, opts, params)
		if err != nil {
			return err
		}
		for _, p := range resp.Query.Pages {
			if e, ok := entryFromPage(p, opts.MaxFileBytes, opts.Still); ok && !opts.Exclude[e.SHA1] {
				seen[e.SHA1] = e
			}
		}
		if resp.BatchComplete {
			pages++
		}
		if len(resp.Continue) == 0 || pages >= opts.PagesPerPrefix {
			return nil
		}
		for k, v := range resp.Continue {
			params.Set(k, v)
		}
	}
	return fmt.Errorf("API kept returning partial results after %d requests", opts.PagesPerPrefix*maxRequestsPerPage)
}

func apiQuery(ctx context.Context, opts DiscoverOptions, params url.Values) (*apiResponse, error) {
	var out apiResponse
	err := withRetry(ctx, opts.Retries, opts.Backoff, func() (err error) {
		resp, err := get(ctx, opts.Client, opts.UserAgent, opts.APIURL+"?"+params.Encode())
		if err != nil {
			return err
		}
		defer func() {
			// A close failure only matters when nothing else went wrong.
			if cerr := resp.Body.Close(); cerr != nil && err == nil {
				err = &transportError{fmt.Errorf("close API response body: %w", cerr)}
			}
		}()
		out = apiResponse{}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return &transportError{fmt.Errorf("decode API response: %w", err)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("API error %s: %s", out.Error.Code, out.Error.Info)
	}
	return &out, nil
}

// entryFromPage converts an API page to an Entry, rejecting anything that is
// not a licensed GIF within the size limit, and, unless still is set, any GIF
// that is not animated.
func entryFromPage(p apiPage, maxFileBytes int64, still bool) (Entry, bool) {
	if len(p.ImageInfo) == 0 {
		return Entry{}, false
	}
	ii := p.ImageInfo[0]
	meta := func(key string) string {
		s, _ := ii.ExtMetadata[key].Value.(string)
		return cleanText(s)
	}
	e := Entry{
		Title:          p.Title,
		PageID:         p.PageID,
		URL:            stripQuery(ii.URL),
		DescriptionURL: ii.DescriptionURL,
		SHA1:           ii.SHA1,
		Size:           ii.Size,
		Width:          ii.Width,
		Height:         ii.Height,
		Duration:       ii.Duration,
		License:        meta("LicenseShortName"),
		Artist:         meta("Artist"),
	}
	ok := ii.Mime == "image/gif" &&
		(still || e.Duration > 0) &&
		e.License != "" &&
		e.Size <= maxFileBytes &&
		e.validate() == nil
	return e, ok
}

// stripQuery drops the tracking parameters Commons appends to file URLs.
func stripQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	return u.String()
}

var (
	htmlTag    = regexp.MustCompile(`<[^>]*>`)
	whitespace = regexp.MustCompile(`\s+`)
)

// cleanText reduces the HTML fragments Commons returns for metadata to plain text.
func cleanText(s string) string {
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	return strings.TrimSpace(whitespace.ReplaceAllString(s, " "))
}

// selectEntries picks up to count candidates spread over three size buckets
// (40% small, 40% medium, the rest large) without exceeding maxTotalBytes.
// Candidates are taken in checksum order, which is arbitrary but stable, and
// the result is sorted by checksum.
func selectEntries(candidates []Entry, count int, maxTotalBytes int64) []Entry {
	sorted := append([]Entry(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SHA1 < sorted[j].SHA1 })

	small, medium := count*2/5, count*2/5
	quotas := []int{small, medium, count - small - medium}
	bucket := func(size int64) int {
		switch {
		case size <= smallMaxBytes:
			return 0
		case size <= mediumMaxBytes:
			return 1
		}
		return 2
	}

	var total int64
	picked := make([]Entry, 0, count)
	for b := range quotas {
		for _, e := range sorted {
			if quotas[b] == 0 {
				break
			}
			if bucket(e.Size) != b || total+e.Size > maxTotalBytes {
				continue
			}
			picked = append(picked, e)
			total += e.Size
			quotas[b]--
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].SHA1 < picked[j].SHA1 })
	return picked
}

// BucketCounts reports how many entries fall in the small, medium and large
// size buckets used during selection.
func BucketCounts(entries []Entry) (small, medium, large int) {
	for _, e := range entries {
		switch {
		case e.Size <= smallMaxBytes:
			small++
		case e.Size <= mediumMaxBytes:
			medium++
		default:
			large++
		}
	}
	return small, medium, large
}

// BucketLimits describes the bucket boundaries for display.
func BucketLimits() string {
	return "small <= " + strconv.Itoa(smallMaxBytes>>10) + " KB, medium <= " +
		strconv.Itoa(mediumMaxBytes>>20) + " MB, large above"
}
