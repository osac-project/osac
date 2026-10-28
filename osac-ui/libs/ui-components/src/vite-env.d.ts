/// <reference types="vite/client" />

declare module '*.css';

declare module '*.png' {
  const src: string;
  export default src;
}
