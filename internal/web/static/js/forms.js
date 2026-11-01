// Vacía los formularios marcados tras una respuesta correcta, para que una
// contraseña aplicada no quede en el DOM. Ante un error se conservan los
// campos para corregirlos.
document.addEventListener("htmx:afterRequest", (event) => {
  const form = event.detail.elt;
  if (event.detail.successful && form instanceof HTMLFormElement && form.hasAttribute("data-reset-on-success")) {
    form.reset();
  }
});
