/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        'vibrant-bg': '#0a0a0f',
        'vibrant-bg-hover': '#1a1a2e',
        'vibrant-pink': '#ff00ff',
        'vibrant-pink-light': '#ff66ff',
        'vibrant-purple': '#9d4edd',
        'vibrant-purple-light': '#c77dff',
        'vibrant-sky': '#00d4ff',
        'vibrant-sky-light': '#4dd0e1',
        'vibrant-orange': '#ff6b35',
        'vibrant-cyan': '#00ffff',
        'vibrant-yellow': '#ffff00',
        'vibrant-green': '#00ff00',
        'pinkish-white': '#fff5f5',
        'pinkish-white-hover': '#ffe0e0',
      },
    },
  },
  plugins: [],
}
