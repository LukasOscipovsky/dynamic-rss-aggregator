import React, { useMemo, useState } from "react";
import Sidebar, { ALL, Source } from "./components/sidebar/Sidebar";
import ArticlesGrid from "./components/main/ArticleGrid";

// Curated list of free, publicly available RSS feeds
const SOURCES: Source[] = [
  // Slovak
  { name: "DennikN", url: "https://dennikn.sk/feed", category: "Slovakia" },
  { name: "Sme", url: "https://www.sme.sk/rss", category: "Slovakia" },

  // World news
  { name: "BBC World", url: "https://feeds.bbci.co.uk/news/world/rss.xml", category: "World" },
  { name: "The Guardian World", url: "https://www.theguardian.com/world/rss", category: "World" },
  { name: "NYT World", url: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", category: "World" },
  { name: "Al Jazeera", url: "https://www.aljazeera.com/xml/rss/all.xml", category: "World" },
  { name: "Deutsche Welle", url: "https://rss.dw.com/rdf/rss-en-all", category: "World" },
  { name: "France 24", url: "https://www.france24.com/en/rss", category: "World" },
  { name: "Euronews", url: "https://www.euronews.com/rss", category: "World" },
  { name: "Sky News World", url: "https://feeds.skynews.com/feeds/rss/world.xml", category: "World" },
  { name: "Washington Post World", url: "https://feeds.washingtonpost.com/rss/world", category: "World" },

  // Top stories
  { name: "NYT Home", url: "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml", category: "Top stories" },
  { name: "CNN", url: "http://rss.cnn.com/rss/edition.rss", category: "Top stories" },
  { name: "NPR News", url: "https://feeds.npr.org/1001/rss.xml", category: "Top stories" },
  { name: "CBS News", url: "https://www.cbsnews.com/latest/rss/main", category: "Top stories" },
  { name: "ABC News", url: "https://abcnews.go.com/abcnews/topstories", category: "Top stories" },

  // Business
  { name: "CNBC", url: "https://www.cnbc.com/id/100003114/device/rss/rss.html", category: "Business" },

  // Technology
  { name: "BBC Technology", url: "https://feeds.bbci.co.uk/news/technology/rss.xml", category: "Tech" },
  { name: "The Guardian Technology", url: "https://www.theguardian.com/technology/rss", category: "Tech" },
  { name: "TechCrunch", url: "https://techcrunch.com/feed/", category: "Tech" },
  { name: "The Verge", url: "https://www.theverge.com/rss/index.xml", category: "Tech" },
  { name: "Ars Technica", url: "https://feeds.arstechnica.com/arstechnica/index", category: "Tech" },
  { name: "Wired", url: "https://www.wired.com/feed/rss", category: "Tech" },
  { name: "Hacker News", url: "https://news.ycombinator.com/rss", category: "Tech" },

  // Science
  { name: "ScienceDaily", url: "https://www.sciencedaily.com/rss/all.xml", category: "Science" },
  { name: "NASA", url: "https://www.nasa.gov/news-release/feed/", category: "Science" },

  // Sports
  { name: "ESPN", url: "https://www.espn.com/espn/rss/news", category: "Sports" },
];

const App: React.FC = () => {
  // ALL, a category name, or a single source URL
  const [selected, setSelected] = useState<string>(ALL);

  const selectedSources = useMemo(
    () =>
      SOURCES.filter(
        (s) => selected === ALL || s.category === selected || s.url === selected
      ),
    [selected]
  );

  const title =
    selected === ALL
      ? "Top headlines"
      : SOURCES.find((s) => s.url === selected)?.name ?? selected;

  return (
    <div className="layout">
      <Sidebar sources={SOURCES} selected={selected} onSelect={setSelected} />

      <main className="content">
        <ArticlesGrid title={title} sources={selectedSources} />
      </main>
    </div>
  );
};

export default App;
