package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Page is one page of a list endpoint. Total comes from the X-Total header,
// which is zero when the server does not report a count. Fetched counts the
// rows the endpoint returned before any client-side filtering, so a caller can
// page with it as the next offset.
type Page[T any] struct {
	Items   []T
	Total   int
	Fetched int
}

// CloudFile is the attachment and picture shape served by Drive.
type CloudFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// Publisher is the author of a post: an account, a managed publisher or a
// mirrored fediverse actor.
type Publisher struct {
	ID          string     `json:"id"`
	Type        int        `json:"type"`
	Name        string     `json:"name"`
	Nick        string     `json:"nick"`
	Bio         string     `json:"bio"`
	AccountID   string     `json:"account_id"`
	DisplayName string     `json:"display_name"`
	Picture     *CloudFile `json:"picture"`
	IsFollowing bool       `json:"is_following"`
}

// Handle is the "@name" handle, or the bare name when none is set.
func (p Publisher) Handle() string {
	if p.Name == "" {
		return "unknown"
	}
	return p.Name
}

// Display prefers the nickname, then the actor display name, then the name.
func (p Publisher) Display() string {
	for _, candidate := range []string{p.Nick, p.DisplayName, p.Name} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return p.Handle()
}

// TimelineEvent is one entry of GET /sphere/timeline. A post event carries the
// post in Data; presence, status and discovery events carry their own payloads.
type TimelineEvent struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	ResourceIdentifier string `json:"resource_identifier"`
	Data               Post   `json:"data"`
}

// IsPost reports whether the event carries a post.
func (e TimelineEvent) IsPost() bool {
	return strings.HasPrefix(e.Type, "posts.new")
}

// TimelinePage is one page of the ranked activity timeline.
type TimelinePage struct {
	Items      []TimelineEvent `json:"items"`
	NextCursor string          `json:"next_cursor"`
	Mode       string          `json:"mode"`
}

// Posts returns the posts of the page, dropping the non-post activities.
func (p TimelinePage) Posts() []Post {
	posts := make([]Post, 0, len(p.Items))
	for _, event := range p.Items {
		if event.IsPost() && event.Data.ID != "" {
			posts = append(posts, event.Data)
		}
	}
	return posts
}

// Post is one sphere post: a status, an article, a reply or a repost.
type Post struct {
	ID            string          `json:"id"`
	Title         *string         `json:"title"`
	Description   *string         `json:"description"`
	Content       *string         `json:"content"`
	Language      *string         `json:"language"`
	Slug          *string         `json:"slug"`
	Type          int             `json:"type"`
	Visibility    int             `json:"visibility"`
	PublishedAt   *time.Time      `json:"published_at"`
	CreatedAt     *time.Time      `json:"created_at"`
	EditedAt      *time.Time      `json:"edited_at"`
	PublisherID   string          `json:"publisher_id"`
	Publisher     *Publisher      `json:"publisher"`
	RepliedPostID *string         `json:"replied_post_id"`
	ChainedPostID *string         `json:"chained_post_id"`
	Attachments   []CloudFile     `json:"attachments"`
	Reactions     map[string]int  `json:"reactions_count"`
	ReactionsMade map[string]bool `json:"reactions_made"`
	RepliesCount  int             `json:"replies_count"`
	Upvotes       int             `json:"upvotes"`
	Downvotes     int             `json:"downvotes"`
	BoostCount    int             `json:"boost_count"`
	ViewsTotal    int             `json:"views_total"`
	IsBookmarked  bool            `json:"is_bookmarked"`
	IsTruncated   bool            `json:"is_truncated"`
}

// Body is the text of the post, empty when the server sent none.
func (p Post) Body() string {
	if p.Content != nil {
		return *p.Content
	}
	return ""
}

// AuthorName is the display name of the author.
func (p Post) AuthorName() string {
	if p.Publisher != nil {
		return p.Publisher.Display()
	}
	return "unknown"
}

// AuthorHandle is the "@name" handle of the author.
func (p Post) AuthorHandle() string {
	if p.Publisher != nil {
		return p.Publisher.Handle()
	}
	return "unknown"
}

// When is the publication time, falling back to creation.
func (p Post) When() time.Time {
	if p.PublishedAt != nil {
		return *p.PublishedAt
	}
	if p.CreatedAt != nil {
		return *p.CreatedAt
	}
	return time.Time{}
}

// NewPost is the payload of POST /sphere/posts.
type NewPost struct {
	Content   string `json:"content"`
	Publisher string `json:"-"`
	ReplyTo   string `json:"-"`
	Title     string `json:"-"`
	// Visibility is 0 for a public post.
	Visibility int
}

// FileURL is the public download URL of a Drive file. The endpoint redirects to
// the storage backend, so it can be handed to a browser or an image decoder.
func (c *Client) FileURL(id string) string {
	return c.rootEndpoint("/drive/files/" + url.PathEscape(id))
}

// Timeline lists the ranked activity timeline. cursor is the previous page's
// NextCursor; empty asks for the first page. mode is personalized, top or
// latest, and aggressive enables the server's stricter ranking.
//
// Pages made only of presence, status or discovery events are skipped so a
// caller always gets a page that either holds posts or ends the timeline.
func (c *Client) Timeline(ctx context.Context, cursor string, take int, mode string, aggressive bool) (TimelinePage, error) {
	if take <= 0 {
		take = 20
	}
	if mode == "" {
		mode = "personalized"
	}
	var page TimelinePage
	for range 4 {
		query := url.Values{}
		query.Set("take", strconv.Itoa(take))
		query.Set("mode", mode)
		query.Set("aggressive", strconv.FormatBool(aggressive))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		page = TimelinePage{}
		if err := c.doRoot(ctx, http.MethodGet, "/sphere/timeline?"+query.Encode(), nil, &page); err != nil {
			return TimelinePage{}, err
		}
		if len(page.Posts()) > 0 || page.NextCursor == "" {
			return page, nil
		}
		cursor = page.NextCursor
	}
	return page, nil
}

// Bookmarks lists the signed-in account's bookmarks.
func (c *Client) Bookmarks(ctx context.Context, offset, take int) (Page[Post], error) {
	var items []Post
	meta, err := c.doRootPage(ctx, http.MethodGet,
		pagePath("/sphere/posts/bookmarks", offset, take)+"&order=created", nil, &items)
	return Page[Post]{Items: items, Total: totalFrom(meta.Header)}, err
}

// Post fetches one post by ID.
func (c *Client) Post(ctx context.Context, id string) (Post, error) {
	var post Post
	err := c.doRoot(ctx, http.MethodGet, "/sphere/posts/"+id, nil, &post)
	return post, err
}

// Replies lists the replies to a post, newest first.
func (c *Client) Replies(ctx context.Context, id string, offset, take int) (Page[Post], error) {
	var items []Post
	meta, err := c.doRootPage(ctx, http.MethodGet,
		pagePath("/sphere/posts/"+id+"/replies", offset, take), nil, &items)
	return Page[Post]{Items: items, Total: totalFrom(meta.Header)}, err
}

// CreatePost publishes a post or a reply. A reply does not need a publisher:
// the server answers as the account's primary publisher.
func (c *Client) CreatePost(ctx context.Context, req NewPost) (Post, error) {
	body := map[string]any{"content": req.Content, "visibility": req.Visibility}
	if req.ReplyTo != "" {
		body["replied_post_id"] = req.ReplyTo
	}
	if req.Title != "" {
		body["title"] = req.Title
	}
	path := "/sphere/posts"
	if req.Publisher != "" {
		path += "?pub=" + url.QueryEscape(req.Publisher)
	}
	var post Post
	err := c.doRoot(ctx, http.MethodPost, path, body, &post)
	return post, err
}

// PostPatch is a partial update of a post. Nil fields are left untouched.
type PostPatch struct {
	Content     *string
	Title       *string
	Description *string
	Visibility  *int
	// Publisher is the publisher the edit is made as; empty means the
	// account's default publisher.
	Publisher string
}

// UpdatePost edits a post (PATCH /sphere/posts/{id}).
func (c *Client) UpdatePost(ctx context.Context, id string, patch PostPatch) (Post, error) {
	body := map[string]any{}
	if patch.Content != nil {
		body["content"] = *patch.Content
	}
	if patch.Title != nil {
		body["title"] = *patch.Title
	}
	if patch.Description != nil {
		body["description"] = *patch.Description
	}
	if patch.Visibility != nil {
		body["visibility"] = *patch.Visibility
	}
	path := "/sphere/posts/" + id
	if patch.Publisher != "" {
		path += "?pub=" + url.QueryEscape(patch.Publisher)
	}
	var post Post
	err := c.doRoot(ctx, http.MethodPatch, path, body, &post)
	return post, err
}

// DeletePost removes a post (DELETE /sphere/posts/{id}).
func (c *Client) DeletePost(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodDelete, "/sphere/posts/"+id, nil, nil)
}

// ToggleReaction adds a reaction, or removes it when the account already made
// one with the same symbol. It reports whether the reaction is now present.
//
// The server treats the POST as a toggle and answers 204 when it removed the
// reaction, so the status code is the only signal.
func (c *Client) ToggleReaction(ctx context.Context, postID, symbol string) (bool, error) {
	meta, err := c.doRootStatus(ctx, http.MethodPost, "/sphere/posts/"+postID+"/reactions",
		map[string]any{"symbol": symbol, "attitude": 0})
	if err != nil {
		return false, err
	}
	return meta.Status != http.StatusNoContent, nil
}

// Bookmark saves a post for the signed-in account.
func (c *Client) Bookmark(ctx context.Context, postID string) error {
	return c.doRoot(ctx, http.MethodPost, "/sphere/posts/"+postID+"/bookmark", nil, nil)
}

// Unbookmark removes a saved post.
func (c *Client) Unbookmark(ctx context.Context, postID string) error {
	return c.doRoot(ctx, http.MethodDelete, "/sphere/posts/"+postID+"/bookmark", nil, nil)
}

// Publishers lists the publishers the signed-in account can post as.
func (c *Client) Publishers(ctx context.Context) ([]Publisher, error) {
	var items []Publisher
	err := c.doRoot(ctx, http.MethodGet, "/sphere/publishers", nil, &items)
	return items, err
}

// pagePath builds a paginated path with offset and take.
func pagePath(path string, offset, take int) string {
	if take <= 0 {
		take = 20
	}
	if offset < 0 {
		offset = 0
	}
	return path + "?offset=" + strconv.Itoa(offset) + "&take=" + strconv.Itoa(take)
}

// totalFrom reads the pagination total header.
func totalFrom(header http.Header) int {
	if header == nil {
		return 0
	}
	n, _ := strconv.Atoi(header.Get("X-Total"))
	return n
}
