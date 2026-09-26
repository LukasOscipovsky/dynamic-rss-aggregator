import React, { useEffect, useState } from "react";

export interface Article {
  title: string;
  link: string;
  description: string;
  published: string;
}

interface ArticlesGridProps {
  sourceUrl: string;
}

const ArticlesGrid: React.FC<ArticlesGridProps> = ({ sourceUrl }) => {
  const [articles, setArticles] = useState<Article[]>([]);

  useEffect(() => {
    async function loadArticles() {
      try {
        const response = await fetch(`http://localhost:8080/articles?source=${encodeURIComponent(sourceUrl)}`);
        const data: Article[] = await response.json();
        setArticles(data);
      } catch (error) {
        console.error("Failed to fetch articles:", error);
      }
    }

    loadArticles();
  }, [sourceUrl]);

  const getImage = (description: string): string | null => {
    const match = description.match(/src="([^"]+)"/);
    return match ? match[1] : null;
  };

  const stripHtml = (html: string): string => {
    const tmp = document.createElement("DIV");
    tmp.innerHTML = html;
    return tmp.textContent || tmp.innerText || "";
  };

  return (
    <div>
      <h1 style={{ marginBottom: "20px" }}>Articles</h1>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))",
          gap: "20px",
        }}
      >
        {articles.map((article, index) => {
          const image = getImage(article.description);
          return (
            <div
              key={index}
              style={{
                border: "1px solid #ddd",
                borderRadius: "8px",
                overflow: "hidden",
                display: "flex",
                flexDirection: "column",
                background: "#fff",
                transition: "transform 0.2s",
              }}
              onMouseEnter={(e) => (e.currentTarget.style.transform = "scale(1.03)")}
              onMouseLeave={(e) => (e.currentTarget.style.transform = "scale(1)")}
            >
              {image && (
                <img
                  src={image}
                  alt=""
                  style={{ width: "100%", height: "160px", objectFit: "cover" }}
                />
              )}
              <div style={{ padding: "15px", flexGrow: 1, display: "flex", flexDirection: "column" }}>
                <h2 style={{ fontSize: "18px", marginBottom: "10px", flexGrow: 0 }}>
                  <a href={article.link} target="_blank" rel="noopener noreferrer" style={{ textDecoration: "none", color: "#111" }}>
                    {article.title}
                  </a>
                </h2>
                <p style={{ flexGrow: 1, fontSize: "14px", color: "#555" }}>
                  {stripHtml(article.description).slice(0, 120)}...
                </p>
                <small style={{ marginTop: "auto", color: "#999" }}>
                  {new Date(article.published).toLocaleDateString()}
                </small>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default ArticlesGrid;