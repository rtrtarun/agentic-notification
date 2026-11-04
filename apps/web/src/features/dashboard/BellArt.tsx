/** The hero's illustration: a bell with a soft orbit. Purely decorative. */
export function BellArt() {
  return (
    <svg className="bell-art" viewBox="0 0 220 160" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id="bell-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#8f7bff" />
          <stop offset="1" stopColor="#4389ff" />
        </linearGradient>
      </defs>
      <ellipse cx="112" cy="84" rx="92" ry="52" fill="none" stroke="#8f7bff" strokeOpacity="0.35" transform="rotate(-18 112 84)" />
      <circle cx="30" cy="96" r="4" fill="#a9c8ff" />
      <circle cx="190" cy="42" r="5" fill="#c9bcff" />
      <path d="M112 22c-22 0-36 17-36 40 0 26-9 33-14 40h100c-5-7-14-14-14-40 0-23-14-40-36-40Z" fill="url(#bell-g)" />
      <path d="M112 22c-10 4-16 18-16 40 0 26-4 33-6 40" fill="none" stroke="#fff" strokeOpacity="0.28" strokeWidth="4" strokeLinecap="round" />
      <circle cx="112" cy="18" r="6" fill="#6b4df0" />
      <path d="M98 106a14 14 0 0 0 28 0" fill="#5b3fd0" />
    </svg>
  )
}
