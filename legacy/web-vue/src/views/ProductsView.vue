<template>
    <div>
        <ProductList/>
    </div>
</template>

<script>
    import ProductList from "../components/ProductList.vue";

    export default {
        name: 'products',
        components: {
            ProductList
        }
    }
</script>
