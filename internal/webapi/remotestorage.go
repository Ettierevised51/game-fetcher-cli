package webapi

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

// Mod is a workshop item.
type Mod struct {
	ID            string
	Title         string
	ConsumerAppID int
	TimeUpdated   int64
}

type modDetails struct {
	PublishedFileID string `json:"publishedfileid"`
	Result          int    `json:"result"`
	Title           string `json:"title"`
	// Some Steam endpoints use consumer_appid, GetPublishedFileDetails
	// uses consumer_app_id. Accept both.
	ConsumerAppID    int   `json:"consumer_appid"`
	ConsumerAppIDAlt int   `json:"consumer_app_id"`
	TimeUpdated      int64 `json:"time_updated"`
}

func (d modDetails) toMod() Mod {
	consumer := d.ConsumerAppID
	if consumer == 0 {
		consumer = d.ConsumerAppIDAlt
	}
	return Mod{ID: d.PublishedFileID, Title: d.Title, ConsumerAppID: consumer, TimeUpdated: d.TimeUpdated}
}

func idsForm(ids []string) url.Values {
	form := url.Values{}
	form.Set("itemcount", strconv.Itoa(len(ids)))
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	return form
}

// PublishedFileDetails fetches metadata for known workshop item ids via the
// keyless ISteamRemoteStorage/GetPublishedFileDetails (verified 2026-07).
// Items the API does not report cleanly (result != 1) are omitted.
func (c *Client) PublishedFileDetails(ctx context.Context, ids []string) ([]Mod, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var parsed struct {
		Response struct {
			PublishedFileDetails []modDetails `json:"publishedfiledetails"`
		} `json:"response"`
	}
	err := c.postForm(ctx, c.baseURL()+"/ISteamRemoteStorage/GetPublishedFileDetails/v1/", idsForm(ids), &parsed)
	if err != nil {
		return nil, err
	}
	out := make([]Mod, 0, len(parsed.Response.PublishedFileDetails))
	for _, d := range parsed.Response.PublishedFileDetails {
		if d.Result == 1 {
			out = append(out, d.toMod())
		}
	}
	return out, nil
}

// TimesUpdated returns time_updated per workshop item id (keyless).
func (c *Client) TimesUpdated(ctx context.Context, ids []string) (map[string]int64, error) {
	mods, err := c.PublishedFileDetails(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(mods))
	for _, m := range mods {
		out[m.ID] = m.TimeUpdated
	}
	return out, nil
}

// GetCollectionDetails expands a workshop collection into its child item ids
// (in the collection's sort order) via the keyless
// ISteamRemoteStorage/GetCollectionDetails (envelope verified 2026-07).
func (c *Client) GetCollectionDetails(ctx context.Context, collectionID string) ([]string, error) {
	form := url.Values{}
	form.Set("collectioncount", "1")
	form.Set("publishedfileids[0]", collectionID)
	var parsed struct {
		Response struct {
			CollectionDetails []struct {
				PublishedFileID string `json:"publishedfileid"`
				Result          int    `json:"result"`
				Children        []struct {
					PublishedFileID string `json:"publishedfileid"`
					SortOrder       int    `json:"sortorder"`
				} `json:"children"`
			} `json:"collectiondetails"`
		} `json:"response"`
	}
	err := c.postForm(ctx, c.baseURL()+"/ISteamRemoteStorage/GetCollectionDetails/v1/", form, &parsed)
	if err != nil {
		return nil, err
	}
	if len(parsed.Response.CollectionDetails) == 0 {
		return nil, fmt.Errorf("collection %s: empty response", collectionID)
	}
	detail := parsed.Response.CollectionDetails[0]
	if detail.Result != 1 {
		return nil, fmt.Errorf("collection %s: not found or not a collection (result %d)", collectionID, detail.Result)
	}
	children := detail.Children
	sort.SliceStable(children, func(i, j int) bool { return children[i].SortOrder < children[j].SortOrder })
	ids := make([]string, 0, len(children))
	for _, ch := range children {
		if ch.PublishedFileID != "" {
			ids = append(ids, ch.PublishedFileID)
		}
	}
	return ids, nil
}
