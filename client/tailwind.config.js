/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      colors: {
        aura: '#22d3ee',
        bg: '#0b0f14',
        panel: '#111821',
        line: '#1f2a37',
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'Menlo', 'ui-monospace', 'monospace'],
      },
      keyframes: {
        auraPulse: {
          '0%, 100%': { boxShadow: '0 0 0 0 rgba(34, 211, 238, 0.55), 0 0 18px 2px rgba(34, 211, 238, 0.35)' },
          '50%':      { boxShadow: '0 0 0 6px rgba(34, 211, 238, 0.10), 0 0 28px 6px rgba(34, 211, 238, 0.65)' },
        },
      },
      animation: {
        aura: 'auraPulse 1.6s ease-in-out infinite',
      },
    },
  },
  plugins: [],
};
