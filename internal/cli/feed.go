package cli

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

// feedOptions holds the paging flags shared by the listing commands.
type feedOptions struct {
	limit  int
	offset int
	cursor string
	mode   string
}

// feedCommand lists the ranked timeline.
func (a *app) feedCommand() *cobra.Command {
	var opts feedOptions
	cmd := &cobra.Command{
		Use:   "feed",
		Short: "List the ranked timeline",
		Long: "Lists the ranked activity timeline. No session is needed, so this works\n" +
			"before signing in and in scripts that only read.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runFeed(cmd.Context(), opts)
		},
	}
	cmd.Flags().IntVar(&opts.limit, "limit", 20, "number of posts to print")
	cmd.Flags().StringVar(&opts.cursor, "cursor", "", "timeline cursor from a previous page")
	cmd.Flags().StringVar(&opts.mode, "mode", "personalized", "ranking: personalized, top or latest")
	return cmd
}

// runFeed prints one page of the timeline.
func (a *app) runFeed(ctx context.Context, opts feedOptions) error {
	if err := a.prepare(); err != nil {
		return err
	}
	page, err := a.client.Timeline(ctx, opts.cursor, opts.limit, opts.mode, true)
	if err != nil {
		return err
	}
	if err := a.printPosts(page.Posts(), 0); err != nil {
		return err
	}
	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type": ui.RecordTimeline, "mode": page.Mode, "next_cursor": page.NextCursor,
		})
	}
	if page.NextCursor != "" {
		a.out.Progressf("next cursor: %s", page.NextCursor)
	}
	return nil
}

// printPosts renders a post page as a table or as records.
func (a *app) printPosts(posts []api.Post, total int) error {
	payloads := make([]postPayload, 0, len(posts))
	for _, post := range posts {
		payloads = append(payloads, a.postFrom(post))
	}
	if a.out.JSON() {
		for _, post := range payloads {
			if err := a.out.Emit(map[string]any{"type": ui.RecordPost, "post": post}); err != nil {
				return err
			}
		}
		return nil
	}
	if len(payloads) == 0 {
		fmt.Fprintln(a.stdout, "no posts")
		return nil
	}
	if total > 0 {
		a.out.Progressf("%d of %d posts", len(payloads), total)
	} else {
		a.out.Progressf("%d posts", len(payloads))
	}
	return a.out.Table(ui.RecordPost, postHeaders, postRows(payloads))
}

// showPostCommand reads one post and its replies.
func (a *app) showPostCommand() *cobra.Command {
	var replies int
	cmd := &cobra.Command{
		Use:   "post <id>",
		Short: "Show a post and its replies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runPost(cmd.Context(), args[0], replies)
		},
	}
	cmd.Flags().IntVar(&replies, "replies", 10, "number of replies to print")
	return cmd
}

// runPost prints one post and the first page of its replies.
func (a *app) runPost(ctx context.Context, id string, replyLimit int) error {
	if err := a.prepare(); err != nil {
		return err
	}
	post, err := a.client.Post(ctx, id)
	if err != nil {
		return err
	}
	payload := a.postFrom(post)
	if a.out.JSON() {
		if err := a.out.Emit(map[string]any{"type": ui.RecordPost, "post": payload}); err != nil {
			return err
		}
	} else {
		a.printPostDetail(payload)
	}

	if replyLimit <= 0 {
		return nil
	}
	page, err := a.client.Replies(ctx, id, 0, replyLimit)
	if err != nil {
		return err
	}
	if len(page.Items) == 0 {
		if !a.out.JSON() {
			fmt.Fprintln(a.stdout, "\nno replies")
		}
		return nil
	}
	if a.out.JSON() {
		for _, reply := range page.Items {
			if err := a.out.Emit(map[string]any{
				"type":  ui.RecordReply,
				"post":  payload.ID,
				"reply": a.postFrom(reply),
			}); err != nil {
				return err
			}
		}
		return nil
	}
	fmt.Fprintf(a.stdout, "\nreplies (%d)\n", page.Total)
	return a.out.Table(ui.RecordReply, postHeaders, postRows(postPayloads(a, page.Items)))
}

// postPayloads converts a slice of wire posts.
func postPayloads(a *app, posts []api.Post) []postPayload {
	out := make([]postPayload, 0, len(posts))
	for _, post := range posts {
		out = append(out, a.postFrom(post))
	}
	return out
}

// printPostDetail renders one post for humans.
func (a *app) printPostDetail(post postPayload) {
	fmt.Fprintf(a.stdout, "%s (@%s) · %s\n", post.Author, post.AuthorHandle, shortWhen(post.PublishedAt))
	if post.Title != "" {
		fmt.Fprintf(a.stdout, "\n%s\n", post.Title)
	}
	fmt.Fprintf(a.stdout, "\n%s\n", post.Content)
	if len(post.Attachments) > 0 {
		fmt.Fprintf(a.stdout, "\n%d attachment(s)\n", len(post.Attachments))
	}
	stats := fmt.Sprintf("%d replies", post.Replies)
	if reactions := reactionText(post.Reactions); reactions != "" {
		stats += " · " + reactions
	}
	if post.Bookmarked {
		stats += " · saved"
	}
	fmt.Fprintf(a.stdout, "\n%s\n%s\n", stats, post.ID)
}

// bookmarksCommand lists saved posts.
func (a *app) bookmarksCommand() *cobra.Command {
	var opts feedOptions
	cmd := &cobra.Command{
		Use:   "bookmarks",
		Short: "List saved posts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runBookmarks(cmd.Context(), opts)
		},
	}
	cmd.Flags().IntVar(&opts.limit, "limit", 20, "number of posts to print")
	cmd.Flags().IntVar(&opts.offset, "offset", 0, "pagination offset")
	return cmd
}

// runBookmarks prints the signed-in account's bookmarks.
func (a *app) runBookmarks(ctx context.Context, opts feedOptions) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	page, err := a.client.Bookmarks(ctx, opts.offset, opts.limit)
	if err != nil {
		return err
	}
	return a.printPosts(page.Items, page.Total)
}

// saveCommand toggles a bookmark.
func (a *app) saveCommand() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "save <post-id>",
		Short: "Bookmark a post, or remove the bookmark with --remove",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSave(cmd.Context(), args[0], remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the bookmark instead of adding it")
	return cmd
}

// runSave toggles a bookmark and reports the result.
func (a *app) runSave(ctx context.Context, id string, remove bool) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	if remove {
		if err := a.client.Unbookmark(ctx, id); err != nil {
			return err
		}
	} else if err := a.client.Bookmark(ctx, id); err != nil {
		return err
	}
	saved := !remove
	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":          ui.RecordBookmark,
			"post":          id,
			"is_bookmarked": saved,
		})
	}
	if saved {
		fmt.Fprintf(a.stdout, "saved %s\n", id)
	} else {
		fmt.Fprintf(a.stdout, "removed %s from bookmarks\n", id)
	}
	return nil
}

// reactCommand toggles a reaction.
func (a *app) reactCommand() *cobra.Command {
	var symbol string
	cmd := &cobra.Command{
		Use:   "react <post-id>",
		Short: "Toggle a reaction on a post",
		Long: "Adds a reaction, or removes the account's reaction with the same\n" +
			"symbol when it already exists (the server treats it as a toggle).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReact(cmd.Context(), args[0], symbol)
		},
	}
	cmd.Flags().StringVar(&symbol, "symbol", "heart", "reaction symbol, e.g. heart, thumb_up, clap")
	return cmd
}

// runReact toggles one reaction and reports the new state.
func (a *app) runReact(ctx context.Context, id, symbol string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	on, err := a.client.ToggleReaction(ctx, id, symbol)
	if err != nil {
		return err
	}
	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":       ui.RecordReaction,
			"post":       id,
			"symbol":     symbol,
			"is_present": on,
		})
	}
	if on {
		fmt.Fprintf(a.stdout, "reacted %s to %s\n", symbol, id)
	} else {
		fmt.Fprintf(a.stdout, "removed %s from %s\n", symbol, id)
	}
	return nil
}

// replyCommand publishes a reply.
func (a *app) replyCommand() *cobra.Command {
	var (
		content string
		file    string
	)
	cmd := &cobra.Command{
		Use:   "reply <post-id>",
		Short: "Reply to a post",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReply(cmd.Context(), args[0], content, file)
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "reply text")
	cmd.Flags().StringVar(&file, "file", "", "read the reply text from a file (- for stdin)")
	return cmd
}

// runReply publishes a reply and prints the stored post.
func (a *app) runReply(ctx context.Context, parent, content, file string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	text, err := a.composeText(content, file)
	if err != nil {
		return err
	}
	post, err := a.client.CreatePost(ctx, api.NewPost{Content: text, ReplyTo: parent})
	if err != nil {
		return err
	}
	return a.printCreated(post)
}

// publishOptions holds the publish flags.
type publishOptions struct {
	content     string
	file        string
	publisher   string
	title       string
	description string
	language    string
	slug        string
	kind        string
	visibility  int
	tags        []string
	attachments []string
}

// publishCommand publishes a post; it is mounted as "publish" because "post"
// is the read side.
func (a *app) publishCommand() *cobra.Command {
	var opts publishOptions
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a status, article or blog link",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runPublish(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.content, "content", "", "post text, or the URL of a blog link")
	cmd.Flags().StringVar(&opts.file, "file", "", "read the post text from a file (- for stdin)")
	cmd.Flags().StringVar(&opts.publisher, "publisher", "", "publisher name to post as (default: the account's own)")
	cmd.Flags().StringVar(&opts.title, "title", "", "post title")
	cmd.Flags().StringVar(&opts.description, "description", "", "short description")
	cmd.Flags().StringVar(&opts.language, "language", "", "content language")
	cmd.Flags().StringVar(&opts.slug, "slug", "", "article slug")
	cmd.Flags().StringVar(&opts.kind, "type", "", "post type: status (default), article or blog")
	cmd.Flags().IntVar(&opts.visibility, "visibility", 0, "0 public, 1 friends, 2 unlisted, 3 private")
	cmd.Flags().StringSliceVar(&opts.tags, "tag", nil, "tag slug (repeatable, comma separated)")
	cmd.Flags().StringSliceVar(&opts.attachments, "attach", nil, "local file to upload and attach (repeatable)")
	return cmd
}

// runPublish publishes a post and prints the stored record.
func (a *app) runPublish(ctx context.Context, opts publishOptions) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	text, err := a.composeText(opts.content, opts.file)
	if err != nil {
		return err
	}
	kind, err := parsePostType(opts.kind)
	if err != nil {
		return err
	}
	title := opts.title
	if kind == api.PostTypeArticle && title == "" {
		title = firstMarkdownHeading(text)
	}
	if kind == api.PostTypeBlog {
		if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			return &usageError{msg: "blog posts need an http(s) link in --content"}
		}
		if title == "" {
			title = strings.TrimPrefix(strings.TrimPrefix(text, "https://"), "http://")
			if cut := strings.IndexAny(title, "/?#"); cut >= 0 {
				title = title[:cut]
			}
		}
	}
	attachmentIDs := make([]string, 0, len(opts.attachments))
	for _, path := range opts.attachments {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := a.client.UploadFile(ctx, filepath.Base(path), data, mime.TypeByExtension(filepath.Ext(path)))
		if err != nil {
			return err
		}
		attachmentIDs = append(attachmentIDs, file.ID)
	}
	post, err := a.client.CreatePost(ctx, api.NewPost{
		Content:     text,
		Title:       title,
		Description: opts.description,
		Language:    opts.language,
		Slug:        opts.slug,
		Type:        kind,
		Visibility:  opts.visibility,
		Tags:        opts.tags,
		Attachments: attachmentIDs,
		Publisher:   opts.publisher,
	})
	if err != nil {
		return err
	}
	return a.printCreated(post)
}

// parsePostType maps the --type flag onto Sphere's type codes.
func parsePostType(raw string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "status", "post":
		return api.PostTypeStatus, nil
	case "article":
		return api.PostTypeArticle, nil
	case "blog", "link":
		return api.PostTypeBlog, nil
	default:
		return 0, &usageError{msg: "--type must be status, article or blog"}
	}
}

// firstMarkdownHeading reads the first ATX heading as an article title.
func firstMarkdownHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			if title := strings.TrimSpace(strings.TrimLeft(line, "#")); title != "" {
				return title
			}
		}
	}
	return ""
}

// composeText resolves the post body from a flag, a file or stdin.
func (a *app) composeText(content, file string) (string, error) {
	if content != "" && file != "" {
		return "", &usageError{msg: "choose either --content or --file"}
	}
	if file != "" {
		data, err := a.readSource(file)
		if err != nil {
			return "", err
		}
		content = data
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", &usageError{msg: "the post is empty; pass --content or --file"}
	}
	return content, nil
}

// printCreated reports a published post.
func (a *app) printCreated(post api.Post) error {
	payload := a.postFrom(post)
	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordCreated, "post": payload})
	}
	fmt.Fprintf(a.stdout, "published %s\n", payload.ID)
	if payload.URL != "" {
		fmt.Fprintf(a.stdout, "%s\n", payload.URL)
	}
	return nil
}

// publishersCommand lists the publishers the account can post as.
func (a *app) publishersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "publishers",
		Short: "List the publishers you can post as",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runPublishers(cmd.Context())
		},
	}
}

// runPublishers prints the managed publishers.
func (a *app) runPublishers(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	publishers, err := a.client.Publishers(ctx)
	if err != nil {
		return err
	}
	if a.out.JSON() {
		for _, publisher := range publishers {
			if err := a.out.Emit(map[string]any{
				"type": ui.RecordPublisher,
				"id":   publisher.ID,
				"name": publisher.Name,
				"nick": publisher.Nick,
				"kind": publisher.Type,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	rows := make([][]string, 0, len(publishers))
	for _, publisher := range publishers {
		rows = append(rows, []string{
			publisher.Name,
			publisher.Nick,
			strconv.Itoa(publisher.Type),
			publisher.ID,
		})
	}
	return a.out.Table(ui.RecordPublisher, []string{"name", "nick", "type", "id"}, rows)
}
