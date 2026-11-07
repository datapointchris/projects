# Export plan

The old CMS stores posts as **HTML fragments** in a single table, with images
referenced by absolute URLs. The export has to keep every published URL working,
because search engines and a few old forum threads still link to them. Drafts are
_not_ exported. They stay in the database dump in case one is worth finishing.

## Steps

1. Dump the database from the read replica, never the primary.
2. Convert each fragment to markdown with the script below.
3. Download every image the posts reference into `static/images/<year>/<slug>/`.
4. Rewrite the image links to the new relative paths.
5. Diff a sample of twenty rendered posts against the live site.

## Conversion script

```bash
mysqldump --single-transaction blog posts > posts.sql
python3 convert.py --input posts.sql --output content/posts
rg --count '<img' content/posts | sort -t: -k2 -n | tail -20
```

The converter is idempotent, so a rerun after fixing one post rewrites only that
post. Run it from the repository root, or the relative output path lands somewhere
surprising.

## Where each field goes

| Field | Old CMS | Static site |
| --- | --- | --- |
| Title | `post_title` column | `title` in front matter |
| Date | `post_date`, in server time | `date`, in UTC |
| Slug | `post_name` | the file name |
| Tags | a join through `term_relationships` | `tags` list |

## What went wrong on the first attempt

- Posts before 2014 use `<pre>` blocks with hard tabs, which the converter turned
  into a single long line.
- Three posts embed a video through an iframe. Those stay as raw HTML:
  - the Vimeo ones still play
  - the old Flash one is replaced by a link to the transcript
- The image download hit a rate limit around post 400, so it now sleeps between
  requests and resumes from where it stopped.

## Before calling this done

- [x] Dump taken and copied off the server
- [ ] Every post renders without a converter warning
- [ ] The twenty-post diff reviewed by eye

> Keep the dump for a year. The one time an old post turned out to be missing, the
> dump was the only copy left, and nobody remembered which table it came from.

---

The redirect table comes from this export, so finish it first. The
[hosting notes](https://example.com/hosting-notes) say which headers the new host
needs for the permanent redirects, and the rewrite rule that broke last time was
`/archives/(\d+)/(.*)$`.

One image path is longer than any screen and has to wrap on its own:
static/images/2011/a-very-long-slug-that-the-old-permalink-plugin-generated-from-the-entire-title-of-the-post-and-never-shortened.png

Last line of the note.
