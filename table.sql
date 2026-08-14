CREATE TABLE IF NOT EXISTS public.videos
(
    video_id text COLLATE pg_catalog."default",
    trending_date text COLLATE pg_catalog."default",
    title text COLLATE pg_catalog."default",
    channel_title text COLLATE pg_catalog."default",
    category_id integer,
    publish_time text COLLATE pg_catalog."default",
    tags text COLLATE pg_catalog."default",
    views integer,
    likes integer,
    dislikes integer,
    comment_count integer,
    thumbnail_link text COLLATE pg_catalog."default",
    comments_disabled boolean,
    ratings_disabled boolean,
    video_error_or_removed boolean,
    description text COLLATE pg_catalog."default"
)