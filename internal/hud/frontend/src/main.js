import './lib/styles/theme.css';
import './lib/styles/layout.css';
import App from './App.svelte';
import { mount } from 'svelte';

const app = mount(App, {
  target: document.getElementById('app'),
});

export default app;
