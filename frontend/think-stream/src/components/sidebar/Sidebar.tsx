import React from "react";

export interface Source {
  name: string;
  url: string;
  category: string;
}

export const ALL = "__all__";

// Maps a category to its accent color token defined in index.css
export const categoryColor = (category: string): string =>
  `var(--cat-${category.toLowerCase().replace(/\s+/g, "-")})`;

interface SidebarProps {
  sources: Source[];
  selected: string;
  onSelect: (key: string) => void;
}

const Sidebar: React.FC<SidebarProps> = ({ sources, selected, onSelect }) => {
  const categories = Array.from(new Set(sources.map((s) => s.category)));

  return (
    <aside className="sidebar">
      <div className="brand">
        <span className="brand-logo">◆</span>
        Think Stream
      </div>

      <button
        className={`nav-item all ${selected === ALL ? "active" : ""}`}
        onClick={() => onSelect(ALL)}
      >
        All sources
      </button>

      {categories.map((category) => (
        <div
          key={category}
          className="nav-group"
          style={{ "--cat": categoryColor(category) } as React.CSSProperties}
        >
          <button
            className={`nav-item heading ${selected === category ? "active" : ""}`}
            onClick={() => onSelect(category)}
          >
            {category}
          </button>
          <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {sources
              .filter((s) => s.category === category)
              .map((source) => (
                <li key={source.url}>
                  <button
                    className={`nav-item ${selected === source.url ? "active" : ""}`}
                    onClick={() => onSelect(source.url)}
                  >
                    <span className="dot" />
                    {source.name}
                  </button>
                </li>
              ))}
          </ul>
        </div>
      ))}
    </aside>
  );
};

export default Sidebar;
