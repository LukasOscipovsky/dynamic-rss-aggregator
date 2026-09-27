import React, { useEffect, useState } from "react";
import { Source, categoryColor } from "../sidebar/Sidebar";

export interface Article {
  title: string;
  link: string;
  description: string;
  published: string;
  source?: string;
  category?: string;
  image?: string;
}

interface ArticlesGridProps {
  title: string;
  sources: Source[];
}

async function fetchSource(source: Source): Promise<Article[]> {
  const response = await fetch(`http://localhost:8080/articles?source=${encodeURIComponent(source.url)}`);
  if (!response.ok) {
    throw new Error(`${source.name}: HTTP ${response.status}`);
  }
  const data: Article[] = await response.json();
  return (data ?? []).map((article) => ({ ...article, source: source.name, category: source.category }));
}

const ArticlesGrid: React.FC<ArticlesGridProps> = ({ title, sources }) => {
  const [articles, setArticles] = useState<Article[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;

    async function loadArticles() {
      setLoading(true);
      // Fetch all selected sources in parallel; a failing feed doesn't block the others
      const results = await Promise.allSettled(sources.map(fetchSource));
      if (cancelled) return;

      const merged: Article[] = [];
      results.forEach((result) => {
        if (result.status === "fulfilled") {
          merged.push(...result.value);
        } else {
          console.error("Failed to fetch articles:", result.reason);
        }
      });
      merged.sort((a, b) => new Date(b.published).getTime() - new Date(a.published).getTime());

      setArticles(merged);
      setLoading(false);
    }

    loadArticles();
    return () => {
      cancelled = true;
    };
  }, [sources]);

  const getImage = (article: Article): string | null => {
    if (article.image) return article.image;
    const match = article.description.match(/src="([^"]+)"/);
    return match ? match[1] : null;
  };

  const stripHtml = (html: string): string => {
    const tmp = document.createElement("DIV");
    tmp.innerHTML = html;
    return tmp.textContent || tmp.innerText || "";
  };

  const initials = (name = ""): string =>
    name
      .split(/\s+/)
      .map((word) => word[0])
      .join("")
      .slice(0, 2)
      .toUpperCase();

  return (
    <div>
      <header className="page-header">
        <div>
          <h1 className="page-title">{title}</h1>
          <p className="page-subtitle">
            {sources.length} source{sources.length === 1 ? "" : "s"} · newest first
          </p>
        </div>
        <span className={`live-pill ${loading ? "loading" : ""}`}>
          {loading ? "Fetching latest…" : `${articles.length} articles`}
        </span>
      </header>

      <div className="grid">
        {loading &&
          Array.from({ length: 9 }).map((_, i) => (
            <div key={i} className="skeleton">
              <div className="block media" />
              <div className="block line" />
              <div className="block line" />
              <div className="block line short" />
            </div>
          ))}

        {!loading &&
          articles.map((article, index) => {
            const image = getImage(article);
            const published = new Date(article.published);
            return (
              <a
                key={`${article.link}-${index}`}
                href={article.link}
                target="_blank"
                rel="noopener noreferrer"
                className={`card ${index === 0 ? "featured" : ""}`}
                style={{ "--cat": categoryColor(article.category ?? "") } as React.CSSProperties}
              >
                <div className="card-media">
                  {image ? (
                    <img src={image} alt="" loading="lazy" />
                  ) : (
                    <div className="card-placeholder">{initials(article.source)}</div>
                  )}
                  <span className="badge">
                    <span className="dot" />
                    {article.source}
                  </span>
                </div>
                <div className="card-body">
                  <h2 className="card-title">{stripHtml(article.title)}</h2>
                  <p className="card-excerpt">{stripHtml(article.description)}</p>
                  <div className="card-meta">
                    <span>{article.category}</span>
                    <time dateTime={published.toISOString()}>
                      {published.toLocaleString(undefined, {
                        dateStyle: "medium",
                        timeStyle: "short",
                      })}
                    </time>
                  </div>
                </div>
              </a>
            );
          })}
      </div>

      {!loading && articles.length === 0 && (
        <div className="empty">No articles could be loaded from the selected sources.</div>
      )}
    </div>
  );
};

export default ArticlesGrid;