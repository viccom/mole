/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        primary: { DEFAULT: '#4F46E5', dark: '#3730A3', light: '#818CF8' },
        sidebar: '#1E1B4B',
        sidebarHover: '#312E81',
      },
    },
  },
  plugins: [
    require('@tailwindcss/typography'),
  ],
}
