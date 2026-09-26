import React, { useState } from "react";
import Sidebar, { Source } from "./components/sidebar/Sidebar";
import ArticlesGrid from "./components/main/ArticleGrid";

const App: React.FC = () => {
  const [sources, setSources] = useState<Source[]>([
    { name: "DennikN", url: "https://dennikn.sk/feed" },
    { name: "Sme", url: "https://www.sme.sk/rss" },
  ]);

  const [selectedSource, setSelectedSource] = useState<string>(sources[0].url);

  return (
    <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", minHeight: "100vh" }}>
      <Sidebar
        sources={sources}
        selectedSource={selectedSource}
        onSelectSource={setSelectedSource}
      />

      <main style={{ padding: "20px", maxWidth: "1400px", margin: "auto" }}>
        <ArticlesGrid sourceUrl={selectedSource} />
      </main>
    </div>
  );
};

export default App;