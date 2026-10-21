-- 0348_tvdb_episode_image_urls (issue #76): TVDB can give an episode image as a path
-- ("/banners/v4/episode/…") instead of a full address. Those were stored as they came, so
-- the browser asked Arrmada for them and every such thumbnail broke. New imports now get
-- TVDB's artwork host in front; this fixes the episodes already stored. Only TVDB artwork
-- paths are touched (TMDB and TVmaze stills are always full addresses).
UPDATE episodes SET still_url = 'https://artworks.thetvdb.com' || still_url
 WHERE still_url LIKE '/banners/%';
UPDATE episodes SET still_url = 'https://artworks.thetvdb.com/' || still_url
 WHERE still_url LIKE 'banners/%';
